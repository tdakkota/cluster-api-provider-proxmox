/*
Copyright 2023-2026 IONOS Cloud.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package goproxmox implements a client for Proxmox resource lifecycle management.
package goproxmox

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/go-logr/logr"
	"github.com/luthermonson/go-proxmox"
	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/ionos-cloud/cluster-api-provider-proxmox/api/v1alpha2"
	capmox "github.com/ionos-cloud/cluster-api-provider-proxmox/pkg/proxmox"
)

var _ capmox.Client = &APIClient{}

// ErrVMIDFree is returned if the VMID is free.
var ErrVMIDFree = errors.New("VMID is free")

// APIClient Proxmox API client object.
type APIClient struct {
	*proxmox.Client
	logger logr.Logger
}

// NewAPIClient initializes a Proxmox API client. If the client is misconfigured, an error is returned.
func NewAPIClient(ctx context.Context, logger logr.Logger, baseURL string, options ...proxmox.Option) (*APIClient, error) {
	proxmoxAPIURL, err := url.JoinPath(baseURL, "api2", "json")
	if err != nil {
		return nil, fmt.Errorf("invalid proxmox base URL %q: %w", baseURL, err)
	}

	options = append(options, proxmox.WithLogger(capmox.Logger{}))
	upstreamClient := proxmox.NewClient(proxmoxAPIURL, options...)
	version, err := upstreamClient.Version(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to initialize proxmox api client: %w", err)
	}
	logger.Info("Proxmox client initialized")
	logger.Info("Proxmox server", "version", version.Release)

	return &APIClient{
		Client: upstreamClient,
		logger: logger,
	}, nil
}

// CloneVM clones a VM based on templateID and VMCloneRequest.
func (c *APIClient) CloneVM(ctx context.Context, templateID int, clone capmox.VMCloneRequest) (capmox.VMCloneResponse, error) {
	// get the node
	node := (&proxmox.Node{}).New(c.Client, clone.Node)
	if err := node.Status(ctx); err != nil {
		return capmox.VMCloneResponse{}, fmt.Errorf("cannot find node with name %s: %w", clone.Node, err)
	}

	// get the vm template
	vmTemplate, err := node.VirtualMachine(ctx, templateID)
	if err != nil {
		return capmox.VMCloneResponse{}, fmt.Errorf("unable to find vm template: %w", err)
	}

	vmOptions := proxmox.VirtualMachineCloneOptions{
		NewID:       clone.NewID,
		Description: clone.Description,
		Format:      clone.Format,
		Full:        proxmox.IntOrBool(clone.Full),
		Name:        clone.Name,
		Pool:        clone.Pool,
		SnapName:    clone.SnapName,
		Storage:     clone.Storage,
		Target:      clone.Target,
	}
	newID, task, err := vmTemplate.Clone(ctx, &vmOptions)
	if err != nil {
		return capmox.VMCloneResponse{}, fmt.Errorf("unable to create new vm: %w", err)
	}

	return capmox.VMCloneResponse{NewID: int64(newID), Task: task}, nil
}

// ConfigureVM updates a VMs settings.
func (c *APIClient) ConfigureVM(ctx context.Context, vm *proxmox.VirtualMachine, options ...capmox.VirtualMachineOption) (*proxmox.Task, error) {
	task, err := vm.Config(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("unable to configure vm: %w", err)
	}
	return task, nil
}

// GetVM returns a VM based on nodeName and vmID.
func (c *APIClient) GetVM(ctx context.Context, nodeName string, vmID int64) (*proxmox.VirtualMachine, error) {
	node := (&proxmox.Node{}).New(c.Client, nodeName)
	if err := node.Status(ctx); err != nil {
		return nil, fmt.Errorf("cannot find node with name %s: %w", nodeName, err)
	}

	vm, err := node.VirtualMachine(ctx, int(vmID))
	if err != nil {
		return nil, fmt.Errorf("cannot find vm with id %d: %w", vmID, err)
	}

	return vm, nil
}

// FindVMResource tries to find a VM by its ID on the whole cluster.
func (c *APIClient) FindVMResource(ctx context.Context, vmID uint64) (*proxmox.ClusterResource, error) {
	cluster, err := c.Cluster(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot get cluster status: %w", err)
	}

	vmResources, err := cluster.Resources(ctx, "vm")
	if err != nil {
		return nil, fmt.Errorf("could not list vm resources: %w", err)
	}

	for _, vm := range vmResources {
		if vm.VMID == vmID {
			return vm, nil
		}
	}

	return nil, fmt.Errorf("unable to find VM with ID %d on any of the nodes", vmID)
}

// normalizeTemplateTags lowercases, sorts and dedupes tags, returning a new
// slice. Proxmox VM tags are always lowercase, and lowercasing can collide, so
// the duplicates have to go before they are counted or joined. It copies rather
// than sorting in place because callers keep using the slice they passed.
func normalizeTemplateTags(tags []string) []string {
	out := make([]string, len(tags))
	for i, tag := range tags {
		out[i] = strings.ToLower(tag)
	}

	slices.Sort(out)

	return slices.Compact(out)
}

// FindVMTemplateByTags tries to find a VMID by its tags across the whole cluster.
//
// It requires the tags to identify exactly one template. Clusters whose nodes
// each keep their own copy of a template legitimately match more than once --
// see [APIClient.FindVMTemplatesByTags], which returns every copy so the caller
// can pick the one co-located with the node it intends to clone onto.
func (c *APIClient) FindVMTemplateByTags(ctx context.Context, templateTags []string, matchPolicy string) (string, int32, error) {
	templates, err := c.FindVMTemplatesByTags(ctx, templateTags, matchPolicy)
	if err != nil {
		return "", -1, err
	}

	// Normalised, so the message names the tags actually matched on rather than
	// whatever spelling and ordering the caller passed.
	templateTags = normalizeTemplateTags(templateTags)

	matches := 0
	for _, vmIDs := range templates {
		matches += len(vmIDs)
	}

	if matches != 1 {
		return "", -1, fmt.Errorf("%w: found %d VM templates with tags %q", ErrTemplateNotFound, matches, strings.Join(templateTags, ";"))
	}

	for node, vmIDs := range templates {
		return node, vmIDs[0], nil
	}

	return "", -1, fmt.Errorf("%w: found 0 VM templates with tags %q", ErrTemplateNotFound, strings.Join(templateTags, ";"))
}

// FindVMTemplatesByTags finds every VM template matching templateTags, keyed by
// the node that holds it.
//
// Without shared storage a template cannot be cloned across nodes, so a cluster
// keeps one identically tagged copy per node and the tags match once per node
// by design. Returning all of them lets the caller schedule onto a node that
// actually holds a copy, instead of resolving the template cluster-wide and
// then failing the clone with "VM uses local storage".
//
// Each node's VMIDs are sorted, so a node holding several matching templates
// resolves the same way on every reconcile rather than following the order the
// API happens to list them in.
func (c *APIClient) FindVMTemplatesByTags(ctx context.Context, templateTags []string, matchPolicy string) (map[string][]int32, error) {
	logger := log.FromContext(ctx)

	cluster, err := c.Cluster(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot get cluster status: %w", err)
	}
	vmResources, err := cluster.Resources(ctx, "vm")
	if err != nil {
		return nil, fmt.Errorf("could not list vm resources: %w", err)
	}

	templateTags = normalizeTemplateTags(templateTags)

	templates := make(map[string][]int32)
	bestDistance := int(^uint(0) >> 1)
NEXT_VM:
	for _, vm := range vmResources {
		if vm.Template == 0 || len(vm.Tags) == 0 {
			continue NEXT_VM
		}

		vmTagMap := make(map[string]string)
		for tag := range strings.SplitSeq(vm.Tags, ";") {
			vmTagMap[strings.ToLower(strings.TrimSpace(tag))] = ""
		}

		logger.V(4).Info("VM Template Tags", "Name", vm.Name, "Tags", maps.Values(vmTagMap))

		for _, tag := range templateTags {
			if _, exists := vmTagMap[tag]; !exists {
				continue NEXT_VM
			}
		}

		// distance is always >= 0 because all other cases already jump to NEXT_VM.
		distance := len(vmTagMap) - len(templateTags)
		switch infrav1.TemplateMatchPolicy(matchPolicy) {
		case infrav1.TemplateMatchPolicyExact:
			if distance != 0 {
				continue NEXT_VM
			}
		case infrav1.TemplateMatchPolicyBest:
			if distance > bestDistance {
				continue NEXT_VM
			}
			// A strictly better match invalidates everything collected so far,
			// including copies already found on other nodes.
			if distance < bestDistance {
				bestDistance = distance
				clear(templates)
			}
		}

		templates[vm.Node] = append(templates[vm.Node], int32(vm.VMID))
	}

	if len(templates) == 0 {
		return nil, fmt.Errorf("%w: found 0 VM templates with tags %q", ErrTemplateNotFound, strings.Join(templateTags, ";"))
	}

	for node := range templates {
		slices.Sort(templates[node])
	}

	return templates, nil
}

// DeleteVM deletes a VM based on the nodeName and vmID.
func (c *APIClient) DeleteVM(ctx context.Context, nodeName string, vmID int64) (*proxmox.Task, error) {
	// A vmID can not be lower than 100.
	// If the provided vmID is lower (like -1 in issue #31), just error out without calling the API.
	if vmID < 100 {
		return nil, fmt.Errorf("vm with id %d does not exist", vmID)
	}

	node := (&proxmox.Node{}).New(c.Client, nodeName)
	if err := node.Status(ctx); err != nil {
		return nil, fmt.Errorf("cannot find node with name %s: %w", nodeName, err)
	}

	cluster, err := c.Cluster(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot get cluster")
	}

	if vmidFree, err := cluster.CheckID(ctx, int(vmID)); vmidFree {
		return nil, ErrVMIDFree
	} else if err != nil {
		return nil, err
	}

	vm, err := node.VirtualMachine(ctx, int(vmID))
	if err != nil {
		return nil, fmt.Errorf("cannot find vm with id %d: %w", vmID, err)
	}

	if vm.IsRunning() {
		if _, err = vm.Stop(ctx); err != nil {
			return nil, fmt.Errorf("cannot stop vm id %d: %w", vmID, err)
		}
	}

	task, err := vm.Delete(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot delete vm with id %d: %w", vmID, err)
	}

	return task, nil
}

// CheckID checks if the vmid is available on the cluster.
// Returns true if the vmid is available, false if it is taken.
func (c *APIClient) CheckID(ctx context.Context, vmid int64) (bool, error) {
	cluster, err := c.Cluster(ctx)
	if err != nil {
		return false, fmt.Errorf("cannot get cluster")
	}
	return cluster.CheckID(ctx, int(vmid))
}

// GetTask returns a task associated with upID.
func (c *APIClient) GetTask(ctx context.Context, upID string) (*proxmox.Task, error) {
	task := proxmox.NewTask(proxmox.UPID(upID), c.Client)

	err := task.Ping(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot get task with UPID %s: %w", upID, err)
	}

	return task, nil
}

// GetReservableMemoryBytes returns the memory that can be reserved by a new VM, in bytes.
func (c *APIClient) GetReservableMemoryBytes(ctx context.Context, nodeName string, nodeMemoryAdjustment int64) (uint64, error) {
	node := (&proxmox.Node{}).New(c.Client, nodeName)

	if err := node.Status(ctx); err != nil {
		return 0, fmt.Errorf("cannot find node with name %s: %w", nodeName, err)
	}

	reservableMemory := uint64(float64(node.Memory.Total) / 100 * float64(nodeMemoryAdjustment))

	if nodeMemoryAdjustment == 0 {
		return node.Memory.Total, nil
	}

	vms, err := node.VirtualMachines(ctx)
	if err != nil {
		return 0, fmt.Errorf("cannot list vms for node %s: %w", nodeName, err)
	}

	for _, vm := range vms {
		// Ignore VM Templates, as they can't be started.
		if vm.Template {
			continue
		}
		if reservableMemory < vm.MaxMem {
			reservableMemory = 0
		} else {
			reservableMemory -= vm.MaxMem
		}
	}

	containers, err := node.Containers(ctx)
	if err != nil {
		return 0, fmt.Errorf("cannot list containers for node %s: %w", nodeName, err)
	}

	for _, ct := range containers {
		if reservableMemory < ct.MaxMem {
			reservableMemory = 0
		} else {
			reservableMemory -= ct.MaxMem
		}
	}

	return reservableMemory, nil
}

// ResizeDisk resizes a VM disk to the specified size.
func (c *APIClient) ResizeDisk(ctx context.Context, vm *proxmox.VirtualMachine, disk, size string) (*proxmox.Task, error) {
	return vm.ResizeDisk(ctx, disk, size)
}

// ResumeVM resumes the VM.
func (c *APIClient) ResumeVM(ctx context.Context, vm *proxmox.VirtualMachine) (*proxmox.Task, error) {
	return vm.Resume(ctx)
}

// StartVM starts the VM.
func (c *APIClient) StartVM(ctx context.Context, vm *proxmox.VirtualMachine) (*proxmox.Task, error) {
	return vm.Start(ctx)
}

// TagVM tags the VM.
func (c *APIClient) TagVM(ctx context.Context, vm *proxmox.VirtualMachine, tag string) (*proxmox.Task, error) {
	return vm.AddTag(ctx, tag)
}

// UnmountCloudInitISO unmounts the cloud-init iso from VM.
func (c *APIClient) UnmountCloudInitISO(ctx context.Context, vm *proxmox.VirtualMachine, device string) error {
	err := vm.UnmountCloudInitISO(ctx, device)
	if err != nil {
		return fmt.Errorf("unable to unmount cloud-init iso: %w", err)
	}

	if vm.HasTag(proxmox.MakeTag(proxmox.TagCloudInit)) {
		_, err = vm.RemoveTag(ctx, proxmox.MakeTag(proxmox.TagCloudInit))
	}
	return err
}

// CloudInitStatus returns the cloud-init status of the VM.
func (c *APIClient) CloudInitStatus(ctx context.Context, vm *proxmox.VirtualMachine) (running bool, err error) {
	if err := c.QemuAgentStatus(ctx, vm); err != nil {
		return false, errors.Wrap(err, "error waiting for agent")
	}

	pid, err := vm.AgentExec(ctx, []string{"cloud-init", "status"}, "")
	if err != nil {
		return false, errors.Wrap(err, "unable to get cloud-init status")
	}

	status, err := vm.WaitForAgentExecExit(ctx, pid, 2)
	if err != nil {
		return false, errors.Wrap(err, "unable to wait for agent exec")
	}

	if status.Exited == 1 && status.ExitCode == 0 && strings.Contains(status.OutData, "running") {
		return true, nil
	}
	if status.Exited == 1 && status.ExitCode != 0 {
		return false, ErrCloudInitFailed
	}

	return false, nil
}

// QemuAgentStatus returns the qemu-agent status of the VM.
func (c *APIClient) QemuAgentStatus(ctx context.Context, vm *proxmox.VirtualMachine) error {
	if err := vm.WaitForAgent(ctx, 5); err != nil {
		return errors.Wrap(err, "error waiting for agent")
	}

	return nil
}
