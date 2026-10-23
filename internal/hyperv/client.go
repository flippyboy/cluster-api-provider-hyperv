package hyperv

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

const (
	scriptTimeout = 2 * time.Minute
	chunkSize     = 32 * 1024
)

// Client runs the static PowerShell scripts against one host.
type Client struct {
	session   Session
	transport Transport
}

// NewClient wraps a session. Tests use this with a fake session.
// An empty transport is SSH.
func NewClient(session Session, transport Transport) *Client {
	if transport != TransportWinRM {
		transport = TransportSSH
	}
	return &Client{session: session, transport: transport}
}

// Dial opens an SSH or WinRM session and returns a client.
func Dial(ctx context.Context, cfg HostConfig) (*Client, error) {
	cfg.Transport = cfg.NormalizedTransport()
	var (
		session Session
		err     error
	)
	switch cfg.Transport {
	case TransportWinRM:
		session, err = dialWinRM(cfg)
	default:
		session, err = dialSSH(ctx, cfg)
	}
	if err != nil {
		return nil, err
	}
	return NewClient(session, cfg.Transport), nil
}

// Close closes the underlying session.
func (c *Client) Close() error {
	if c == nil || c.session == nil {
		return nil
	}
	return c.session.Close()
}

type envelope struct {
	Name   string `json:"name"`
	Script string `json:"script"`
	Params any    `json:"params"`
}

func (c *Client) execScript(ctx context.Context, name string, params any, out any) error {
	body, err := loadScript(name)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(envelope{Name: name, Script: body, Params: params})
	if err != nil {
		return fmt.Errorf("encode %s params: %w", name, err)
	}
	ctx, cancel := context.WithTimeout(ctx, scriptTimeout)
	defer cancel()
	stdout, stderr, code, err := c.session.Exec(ctx, powershellCommand(), payload)
	if err != nil {
		return fmt.Errorf("run %s: %w", name, err)
	}
	raw, parseErr := lastJSONObject(stdout)
	if code != 0 {
		msg := ""
		if parseErr == nil {
			if cmdErr := jsonErrorField(raw); cmdErr != nil {
				msg = cmdErr.Error()
			}
		}
		if msg == "" {
			msg = clip(stderr)
		}
		if msg == "" {
			msg = clip(stdout)
		}
		if msg == "" {
			msg = "no output"
		}
		return fmt.Errorf("script %s exited %d: %s", name, code, msg)
	}
	if parseErr != nil {
		return fmt.Errorf("script %s: %w", name, parseErr)
	}
	if err := jsonErrorField(raw); err != nil {
		return fmt.Errorf("script %s: %w", name, err)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("script %s: decode result: %w", name, err)
	}
	return nil
}

// ProbeResult is the host check performed before a cluster is provisioned.
type ProbeResult struct {
	HyperVModule       bool
	SwitchFound        bool
	ImageFound         bool
	ImageIsVHDX        bool
	VirtualMachinePath string
}

type probeParams struct {
	VirtualSwitch string `json:"virtualSwitch"`
	ImagePath     string `json:"imagePath"`
}

type probeJSON struct {
	HyperVModule       bool   `json:"hypervModule"`
	SwitchFound        bool   `json:"switchFound"`
	ImageFound         bool   `json:"imageFound"`
	ImageIsVHDX        bool   `json:"imageIsVhdx"`
	VirtualMachinePath string `json:"virtualMachinePath"`
}

// Probe checks the Hyper-V module, virtual switch, and golden image.
func (c *Client) Probe(ctx context.Context, virtualSwitch, imagePath string) (ProbeResult, error) {
	var raw probeJSON
	if err := c.execScript(ctx, "probe", probeParams{VirtualSwitch: virtualSwitch, ImagePath: imagePath}, &raw); err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{
		HyperVModule:       raw.HyperVModule,
		SwitchFound:        raw.SwitchFound,
		ImageFound:         raw.ImageFound,
		ImageIsVHDX:        raw.ImageIsVHDX,
		VirtualMachinePath: raw.VirtualMachinePath,
	}, nil
}

// VMInput is the desired shape of a new VM. An existing VM is left unchanged.
type VMInput struct {
	Name          string
	VMPath        string
	MemoryMiB     int32
	VCPUs         int32
	VirtualSwitch string
	MACAddress    string
}

// VMResult is the VM Hyper-V reported.
type VMResult struct {
	VMID       string
	VMName     string
	MACAddress string
	VMPath     string
	State      string
}

type ensureVMParams struct {
	VMName        string `json:"vmName"`
	VMPath        string `json:"vmPath,omitempty"`
	MemoryMiB     int32  `json:"memoryMiB"`
	VCPUs         int32  `json:"vCPUs"`
	VirtualSwitch string `json:"virtualSwitch"`
	MACAddress    string `json:"macAddress"`
}

type vmJSON struct {
	VMID       string `json:"vmID"`
	VMName     string `json:"vmName"`
	MACAddress string `json:"macAddress"`
	VMPath     string `json:"vmPath"`
	State      string `json:"state"`
}

// EnsureVM creates the VM when it is missing and returns its identity.
// The MAC in the result is the NIC Hyper-V has, which is the MAC cloud-init must use.
func (c *Client) EnsureVM(ctx context.Context, in VMInput) (VMResult, error) {
	mac, err := HyperVMAC(in.MACAddress)
	if err != nil {
		return VMResult{}, err
	}
	var raw vmJSON
	err = c.execScript(ctx, "ensure-vm", ensureVMParams{
		VMName:        in.Name,
		VMPath:        in.VMPath,
		MemoryMiB:     in.MemoryMiB,
		VCPUs:         in.VCPUs,
		VirtualSwitch: in.VirtualSwitch,
		MACAddress:    mac,
	}, &raw)
	if err != nil {
		return VMResult{}, err
	}
	id, err := ProviderID(raw.VMID)
	if err != nil {
		return VMResult{}, fmt.Errorf("ensure-vm: %w", err)
	}
	colon, err := ColonMAC(raw.MACAddress)
	if err != nil {
		return VMResult{}, fmt.Errorf("ensure-vm mac: %w", err)
	}
	// ProviderID validated the GUID. Store the bare lowercase GUID.
	vmID := id[len("hyperv://"):]
	return VMResult{
		VMID:       vmID,
		VMName:     raw.VMName,
		MACAddress: colon,
		VMPath:     raw.VMPath,
		State:      raw.State,
	}, nil
}

// DiskInput describes the system disk.
type DiskInput struct {
	VMName    string
	ImagePath string
	CloneMode string
	SizeGiB   int32
}

// DiskResult is the disk Hyper-V attached.
type DiskResult struct {
	DiskPath  string
	CloneMode string
}

type ensureDiskParams struct {
	VMName    string `json:"vmName"`
	ImagePath string `json:"imagePath"`
	CloneMode string `json:"cloneMode"`
	SizeGiB   int32  `json:"sizeGiB,omitempty"`
}

type diskJSON struct {
	DiskPath  string `json:"diskPath"`
	CloneMode string `json:"cloneMode"`
}

// EnsureDisk creates a differencing or copy VHDX and attaches it.
func (c *Client) EnsureDisk(ctx context.Context, in DiskInput) (DiskResult, error) {
	var raw diskJSON
	if err := c.execScript(ctx, "ensure-disk", ensureDiskParams{
		VMName:    in.VMName,
		ImagePath: in.ImagePath,
		CloneMode: in.CloneMode,
		SizeGiB:   in.SizeGiB,
	}, &raw); err != nil {
		return DiskResult{}, err
	}
	return DiskResult{DiskPath: raw.DiskPath, CloneMode: raw.CloneMode}, nil
}

// ISOResult reports where the NoCloud ISO was attached.
type ISOResult struct {
	ISOPath      string
	NeedsRestart bool
}

type ensureISOParams struct {
	VMName  string `json:"vmName"`
	ISOPath string `json:"isoPath"`
}

type isoJSON struct {
	ISOPath      string `json:"isoPath"`
	NeedsRestart bool   `json:"needsRestart"`
}

// EnsureISO attaches the NoCloud ISO and sets the boot order when the VM is off.
func (c *Client) EnsureISO(ctx context.Context, vmName, isoPath string) (ISOResult, error) {
	var raw isoJSON
	if err := c.execScript(ctx, "ensure-iso", ensureISOParams{VMName: vmName, ISOPath: isoPath}, &raw); err != nil {
		return ISOResult{}, err
	}
	return ISOResult{ISOPath: raw.ISOPath, NeedsRestart: raw.NeedsRestart}, nil
}

type nameParams struct {
	VMName string `json:"vmName"`
}

type stateJSON struct {
	State string `json:"state"`
}

// Start starts the VM when it is not already running.
func (c *Client) Start(ctx context.Context, vmName string) (string, error) {
	var raw stateJSON
	if err := c.execScript(ctx, "start", nameParams{VMName: vmName}, &raw); err != nil {
		return "", err
	}
	return raw.State, nil
}

// Stop turns the VM off.
func (c *Client) Stop(ctx context.Context, vmName string) (string, error) {
	var raw stateJSON
	if err := c.execScript(ctx, "stop", nameParams{VMName: vmName}, &raw); err != nil {
		return "", err
	}
	return raw.State, nil
}

// InspectResult is the live VM identity, power state, and integration-service addresses.
type InspectResult struct {
	VMID        string
	State       string
	MACAddress  string
	IPAddresses []string
}

type inspectJSON struct {
	VMID        string      `json:"vmID"`
	State       string      `json:"state"`
	MACAddress  string      `json:"macAddress"`
	IPAddresses flexStrings `json:"ipAddresses"`
}

// Inspect reads the VM GUID, power state, MAC, and IP addresses.
func (c *Client) Inspect(ctx context.Context, vmName string) (InspectResult, error) {
	var raw inspectJSON
	if err := c.execScript(ctx, "inspect", nameParams{VMName: vmName}, &raw); err != nil {
		return InspectResult{}, err
	}
	mac := ""
	if raw.MACAddress != "" {
		colon, err := ColonMAC(raw.MACAddress)
		if err != nil {
			return InspectResult{}, fmt.Errorf("inspect mac: %w", err)
		}
		mac = colon
	}
	id := ""
	if raw.VMID != "" {
		providerID, err := ProviderID(raw.VMID)
		if err != nil {
			return InspectResult{}, err
		}
		id = providerID[len("hyperv://"):]
	}
	return InspectResult{
		VMID:        id,
		State:       raw.State,
		MACAddress:  mac,
		IPAddresses: []string(raw.IPAddresses),
	}, nil
}

type deleteParams struct {
	VMName    string `json:"vmName"`
	ImagePath string `json:"imagePath,omitempty"`
}

// Delete removes the VM and its directory. A missing VM is success.
// imagePath is the golden image. The script refuses to delete that directory.
func (c *Client) Delete(ctx context.Context, vmName, imagePath string) error {
	return c.execScript(ctx, "delete", deleteParams{VMName: vmName, ImagePath: imagePath}, nil)
}

// Upload writes data to remotePath.
// SSH uses SFTP. WinRM writes base64 chunks through write-chunk.ps1.
func (c *Client) Upload(ctx context.Context, remotePath string, data []byte) error {
	if c.transport == TransportWinRM {
		return c.uploadChunks(ctx, remotePath, data)
	}
	return c.session.Upload(ctx, remotePath, data)
}

type chunkParams struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	Data   string `json:"data"`
}

func (c *Client) uploadChunks(ctx context.Context, remotePath string, data []byte) error {
	if len(data) == 0 {
		data = []byte{}
	}
	for offset := 0; offset < len(data) || offset == 0; {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[offset:end]
		if err := c.execScript(ctx, "write-chunk", chunkParams{
			Path:   remotePath,
			Offset: int64(offset),
			Data:   base64.StdEncoding.EncodeToString(chunk),
		}, nil); err != nil {
			return fmt.Errorf("upload %s at %d: %w", remotePath, offset, err)
		}
		if end == len(data) {
			break
		}
		offset = end
	}
	return nil
}
