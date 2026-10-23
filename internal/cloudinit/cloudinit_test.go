package cloudinit

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/filesystem"
	"gopkg.in/yaml.v3"
)

const providerID = "hyperv://aabbccdd-eeff-0011-2233-445566778899"

func TestInjectV1Beta3Map(t *testing.T) {
	in := []byte(`#cloud-config
write_files:
  - path: /run/kubeadm/kubeadm.yaml
    content: |
      apiVersion: kubeadm.k8s.io/v1beta3
      kind: ClusterConfiguration
      clusterName: demo
      ---
      apiVersion: kubeadm.k8s.io/v1beta3
      kind: InitConfiguration
      nodeRegistration:
        kubeletExtraArgs:
          cgroup-driver: systemd
`)
	out, err := InjectProviderID(in, providerID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesHas(out, "#cloud-config") {
		t.Fatal("missing header")
	}
	text := fileContent(t, out, "/run/kubeadm/kubeadm.yaml")
	cluster := docByKind(t, text, "ClusterConfiguration")
	init := docByKind(t, text, "InitConfiguration")
	if mapValue(cluster, "clusterName") != "demo" {
		t.Fatal("cluster configuration changed")
	}
	args := mapChild(t, mapChild(t, init, "nodeRegistration"), "kubeletExtraArgs")
	if args.Kind != yaml.MappingNode {
		t.Fatal("expected map")
	}
	if mapValue(args, "provider-id") != providerID || mapValue(args, "cgroup-driver") != "systemd" {
		t.Fatalf("%v", args)
	}
}

func TestInjectV1Beta4ListReplacesExisting(t *testing.T) {
	in := []byte(`#cloud-config
write_files:
  - path: /run/kubeadm/kubeadm.yaml
    content: |
      apiVersion: kubeadm.k8s.io/v1beta4
      kind: JoinConfiguration
      nodeRegistration:
        kubeletExtraArgs:
          - name: provider-id
            value: hyperv://old
          - name: cgroup-driver
            value: systemd
`)
	out, err := InjectProviderID(in, providerID)
	if err != nil {
		t.Fatal(err)
	}
	join := docByKind(t, fileContent(t, out, "/run/kubeadm/kubeadm.yaml"), "JoinConfiguration")
	args := mapChild(t, mapChild(t, join, "nodeRegistration"), "kubeletExtraArgs")
	if args.Kind != yaml.SequenceNode {
		t.Fatal("expected list")
	}
	found := false
	kept := false
	for _, item := range args.Content {
		switch mapValue(item, "name") {
		case "provider-id":
			found = true
			if mapValue(item, "value") != providerID {
				t.Fatal(mapValue(item, "value"))
			}
		case "cgroup-driver":
			kept = mapValue(item, "value") == "systemd"
		}
	}
	if !found || !kept {
		t.Fatal("arg list lost an entry")
	}
}

func TestInjectCreatesNodeRegistration(t *testing.T) {
	in := []byte("#cloud-config\nwrite_files:\n- path: /run/kubeadm/kubeadm.yaml\n  content: |\n    apiVersion: kubeadm.k8s.io/v1beta3\n    kind: InitConfiguration\n")
	out, err := InjectProviderID(in, providerID)
	if err != nil {
		t.Fatal(err)
	}
	init := docByKind(t, fileContent(t, out, "/run/kubeadm/kubeadm.yaml"), "InitConfiguration")
	args := mapChild(t, mapChild(t, init, "nodeRegistration"), "kubeletExtraArgs")
	if mapValue(args, "provider-id") != providerID {
		t.Fatal("provider-id missing")
	}
}

func TestInjectRejectsUnknownDocuments(t *testing.T) {
	cases := [][]byte{
		[]byte("not cloud config\n"),
		[]byte("#cloud-config\npackage_update: true\n"),
		[]byte("#cloud-config\nwrite_files:\n- path: /etc/motd\n  content: hello\n"),
	}
	for _, in := range cases {
		_, err := InjectProviderID(in, providerID)
		if !IsInvalidBootstrap(err) {
			t.Fatalf("got %v", err)
		}
	}
}

func TestISORoundTrip(t *testing.T) {
	user := []byte("#cloud-config\nwrite_files: []\n")
	meta := MetaData("aabbccdd-eeff-0011-2233-445566778899", "capi-demo-cp-0")
	mac := "00:15:5d:aa:bb:cc"
	netcfg := NetworkConfig(mac)
	iso, err := Build(user, meta, netcfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesHas(iso, "cidata") {
		t.Fatal("volume id missing")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "cidata.iso")
	if err := os.WriteFile(path, iso, 0o644); err != nil {
		t.Fatal(err)
	}
	image, err := diskfs.Open(path, diskfs.WithSectorSize(2048))
	if err != nil {
		t.Fatal(err)
	}
	fs, err := image.GetFilesystem(0)
	if err != nil {
		t.Fatal(err)
	}
	label := strings.TrimSpace(fs.Label())
	if !strings.EqualFold(label, "cidata") && !bytesHas(iso, "cidata") {
		t.Fatalf("label %q", label)
	}
	gotUser := readISO(t, fs, "/user-data")
	gotMeta := readISO(t, fs, "/meta-data")
	gotNet := readISO(t, fs, "/network-config")
	if string(gotUser) != string(user) {
		t.Fatalf("user-data %q", gotUser)
	}
	if !bytesHas(gotMeta, "instance-id: aabbccdd-eeff-0011-2233-445566778899") || !bytesHas(gotMeta, "local-hostname: capi-demo-cp-0") {
		t.Fatalf("meta %q", gotMeta)
	}
	if !bytesHas(gotNet, mac) || !bytesHas(gotNet, "dhcp4: true") || !bytesHas(gotNet, "dhcp6: false") {
		t.Fatalf("net %q", gotNet)
	}
}

func bytesHas(b []byte, s string) bool {
	return strings.Contains(string(b), s)
}

func fileContent(t *testing.T, userData []byte, path string) string {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal(userData, &doc); err != nil {
		t.Fatal(err)
	}
	root := mappingOf(&doc)
	files := mapGet(root, "write_files")
	for _, file := range files.Content {
		if mapValue(file, "path") == path {
			return mapValue(file, "content")
		}
	}
	t.Fatalf("missing %s", path)
	return ""
}

func docByKind(t *testing.T, text, kind string) *yaml.Node {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(text))
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			break
		}
		root := mappingOf(&doc)
		if mapValue(root, "kind") == kind {
			return root
		}
	}
	t.Fatalf("missing kind %s in %s", kind, text)
	return nil
}

func mapChild(t *testing.T, n *yaml.Node, key string) *yaml.Node {
	t.Helper()
	got := mapGet(n, key)
	if got == nil {
		t.Fatalf("missing %s", key)
	}
	return got
}

func mapValue(n *yaml.Node, key string) string {
	child := mapGet(n, key)
	if child == nil || child.Kind != yaml.ScalarNode {
		return ""
	}
	return child.Value
}

func readISO(t *testing.T, fs filesystem.FileSystem, name string) []byte {
	t.Helper()
	f, err := fs.OpenFile(name, os.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
