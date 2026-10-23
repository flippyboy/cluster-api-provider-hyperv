package hyperv

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestProbeParsesResult(t *testing.T) {
	fake := &FakeSession{
		Respond: func(name string, params json.RawMessage) ([]byte, []byte, int, error) {
			if name != "probe" {
				t.Fatalf("script %s", name)
			}
			if !strings.Contains(string(params), `"virtualSwitch":"ext"`) {
				t.Fatalf("params %s", params)
			}
			return []byte("noise\n" + `{"hypervModule":true,"switchFound":true,"imageFound":true,"imageIsVhdx":true,"virtualMachinePath":"C:\\VMs"}`), nil, 0, nil
		},
	}
	c := NewClient(fake, TransportSSH)
	got, err := c.Probe(context.Background(), "ext", `D:\images\golden.vhdx`)
	if err != nil {
		t.Fatal(err)
	}
	if !got.SwitchFound || !got.ImageFound || !got.ImageIsVHDX || got.VirtualMachinePath != `C:\VMs` {
		t.Fatalf("%+v", got)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Name != "probe" {
		t.Fatalf("%+v", calls)
	}
}

func TestScriptErrorAndExitCode(t *testing.T) {
	fake := &FakeSession{
		Respond: func(string, json.RawMessage) ([]byte, []byte, int, error) {
			return []byte(`{"error":"switch missing"}`), nil, 1, nil
		},
	}
	c := NewClient(fake, TransportSSH)
	_, err := c.Probe(context.Background(), "ext", "img")
	if err == nil || !strings.Contains(err.Error(), "switch missing") {
		t.Fatal(err)
	}
}

func TestInspectAcceptsStringOrArrayIPs(t *testing.T) {
	for _, stdout := range []string{
		`{"vmID":"{AABBCCDD-EEFF-0011-2233-445566778899}","state":"Running","macAddress":"00155DAABBCC","ipAddresses":"10.0.0.8"}`,
		`{"vmID":"AABBCCDD-EEFF-0011-2233-445566778899","state":"Running","macAddress":"00-15-5D-AA-BB-CC","ipAddresses":["10.0.0.8","169.254.1.1"]}`,
	} {
		fake := &FakeSession{
			Respond: func(string, json.RawMessage) ([]byte, []byte, int, error) {
				return []byte(stdout), nil, 0, nil
			},
		}
		got, err := NewClient(fake, TransportSSH).Inspect(context.Background(), "vm")
		if err != nil {
			t.Fatal(err)
		}
		if got.VMID != "aabbccdd-eeff-0011-2233-445566778899" {
			t.Fatalf("vm id %s", got.VMID)
		}
		if got.MACAddress != "00:15:5d:aa:bb:cc" {
			t.Fatalf("mac %s", got.MACAddress)
		}
		if len(got.IPAddresses) != 2 && len(got.IPAddresses) != 1 {
			t.Fatalf("%v", got.IPAddresses)
		}
		filtered := IPv4Addresses(got.IPAddresses)
		if len(filtered) != 1 || filtered[0] != "10.0.0.8" {
			t.Fatalf("%v", filtered)
		}
	}
}

func TestEnsureDiskRecordsCloneMode(t *testing.T) {
	for _, mode := range []string{"Differencing", "Copy"} {
		fake := &FakeSession{
			Respond: func(name string, params json.RawMessage) ([]byte, []byte, int, error) {
				if name != "ensure-disk" {
					t.Fatalf("script %s", name)
				}
				return []byte(`{"diskPath":"C:\\VMs\\vm\\disk.vhdx","cloneMode":"` + mode + `"}`), nil, 0, nil
			},
		}
		c := NewClient(fake, TransportSSH)
		if _, err := c.EnsureDisk(context.Background(), DiskInput{
			VMName:    "vm",
			ImagePath: `D:\images\golden.vhdx`,
			CloneMode: mode,
		}); err != nil {
			t.Fatal(err)
		}
		var params struct {
			CloneMode string `json:"cloneMode"`
			ImagePath string `json:"imagePath"`
		}
		if err := json.Unmarshal(fake.Calls()[0].Params, &params); err != nil {
			t.Fatal(err)
		}
		if params.CloneMode != mode || params.ImagePath != `D:\images\golden.vhdx` {
			t.Fatalf("%+v", params)
		}
	}
}

func TestDeletePassesImagePath(t *testing.T) {
	fake := &FakeSession{}
	c := NewClient(fake, TransportSSH)
	if err := c.Delete(context.Background(), "capi-c-m", `D:\images\golden.vhdx`); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Name != "delete" {
		t.Fatalf("%+v", calls)
	}
	var params struct {
		VMName    string `json:"vmName"`
		ImagePath string `json:"imagePath"`
	}
	if err := json.Unmarshal(calls[0].Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.VMName != "capi-c-m" || params.ImagePath != `D:\images\golden.vhdx` {
		t.Fatalf("%+v", params)
	}
}

func TestWinRMUploadChunks(t *testing.T) {
	payload := make([]byte, chunkSize+10)
	for i := range payload {
		payload[i] = byte(i)
	}
	fake := &FakeSession{
		Respond: func(name string, params json.RawMessage) ([]byte, []byte, int, error) {
			if name != "write-chunk" {
				t.Fatalf("script %s", name)
			}
			return []byte(`{"written":1,"offset":0}`), nil, 0, nil
		},
	}
	c := NewClient(fake, TransportWinRM)
	if err := c.Upload(context.Background(), `C:\VMs\vm\cidata.iso`, payload); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 2 {
		t.Fatalf("calls %d", len(calls))
	}
	var got []byte
	var offsets []int64
	for _, call := range calls {
		var params struct {
			Path   string `json:"path"`
			Offset int64  `json:"offset"`
			Data   string `json:"data"`
		}
		if err := json.Unmarshal(call.Params, &params); err != nil {
			t.Fatal(err)
		}
		if params.Path != `C:\VMs\vm\cidata.iso` {
			t.Fatal(params.Path)
		}
		raw, err := base64.StdEncoding.DecodeString(params.Data)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, raw...)
		offsets = append(offsets, params.Offset)
	}
	if offsets[0] != 0 || offsets[1] != int64(chunkSize) {
		t.Fatalf("offsets %v", offsets)
	}
	if string(got) != string(payload) {
		t.Fatal("chunk bytes differ")
	}
	if len(fake.Uploads()) != 0 {
		t.Fatal("winrm used sftp upload")
	}
}

func TestSSHUploadUsesSession(t *testing.T) {
	fake := &FakeSession{}
	c := NewClient(fake, TransportSSH)
	if err := c.Upload(context.Background(), `C:\VMs\vm\cidata.iso`, []byte("iso")); err != nil {
		t.Fatal(err)
	}
	ups := fake.Uploads()
	if len(ups) != 1 || ups[0].Path != `C:\VMs\vm\cidata.iso` || string(ups[0].Data) != "iso" {
		t.Fatalf("%+v", ups)
	}
	if len(fake.Calls()) != 0 {
		t.Fatal("ssh upload used a script")
	}
}

func TestLastJSONObjectSkipsBOMAndPrefix(t *testing.T) {
	raw, err := lastJSONObject([]byte("\xef\xbb\xbflog line\n{\"error\":\"x\",\"ok\":true}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := jsonErrorField(raw); err == nil || !strings.Contains(err.Error(), "x") {
		t.Fatal(err)
	}
}

func TestPowerShellBootstrapIsStatic(t *testing.T) {
	cmd := powershellCommand()
	if !strings.HasPrefix(cmd, "powershell.exe -NoProfile -NonInteractive -EncodedCommand ") {
		t.Fatal(cmd)
	}
	enc := strings.TrimPrefix(cmd, "powershell.exe -NoProfile -NonInteractive -EncodedCommand ")
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw)%2 != 0 {
		t.Fatal("utf16 length")
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = uint16(raw[i*2]) | uint16(raw[i*2+1])<<8
	}
	script := string(utf16.Decode(u))
	if !strings.Contains(script, "$ErrorActionPreference = 'Stop'") || !strings.Contains(script, "[Console]::In.ReadToEnd()") {
		t.Fatal(script)
	}
	if strings.Contains(script, "password") {
		t.Fatal("bootstrap mentions a secret")
	}
}
