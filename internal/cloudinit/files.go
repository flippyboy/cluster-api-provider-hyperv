package cloudinit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"

	"gopkg.in/yaml.v3"
)

var ifaceName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,14}$`)

// AddWriteFile adds or replaces one cloud-config write_files entry.
func AddWriteFile(userData []byte, path, content string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("write_files path is empty")
	}
	trimmed := bytes.TrimSpace(userData)
	if !bytes.HasPrefix(trimmed, []byte("#cloud-config")) {
		return nil, &InvalidBootstrapError{Detail: "missing #cloud-config header"}
	}
	dec := yaml.NewDecoder(bytes.NewReader(userData))
	var docs []*yaml.Node
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, &InvalidBootstrapError{Detail: "cloud-config is not yaml"}
		}
		docs = append(docs, &doc)
	}
	if len(docs) == 0 {
		return nil, &InvalidBootstrapError{Detail: "cloud-config is empty"}
	}
	root := mappingOf(docs[0])
	if root == nil {
		return nil, &InvalidBootstrapError{Detail: "cloud-config is not a mapping"}
	}
	files := mapGet(root, "write_files")
	if files == nil {
		files = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		mapSet(root, "write_files", files)
	}
	if files.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("write_files is not a list")
	}
	replaced := false
	for _, file := range files.Content {
		item := mappingOf(file)
		if item == nil || mapScalar(item, "path") != path {
			continue
		}
		mapSet(item, "content", literalNode(content))
		replaced = true
		break
	}
	if !replaced {
		files.Content = append(files.Content, writeFileNode(path, content))
	}
	out, err := encodeDocs(docs)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(out, []byte("#cloud-config")) {
		out = append([]byte("#cloud-config\n"), out...)
	}
	return out, nil
}

// NetworkConfigNamed is NetworkConfig plus a stable interface name.
// kube-vip binds the virtual IP to that name.
func NetworkConfigNamed(mac, iface string) ([]byte, error) {
	if !ifaceName.MatchString(iface) {
		return nil, fmt.Errorf("interface %q is invalid", iface)
	}
	body := string(NetworkConfig(mac))
	needle := "    dhcp4: true\n"
	if !bytes.Contains([]byte(body), []byte(needle)) {
		return nil, fmt.Errorf("network config has no dhcp4 entry")
	}
	return []byte(stringsReplaceOnce(body, needle, "    set-name: "+iface+"\n"+needle)), nil
}

func stringsReplaceOnce(s, old, new string) string {
	idx := bytes.Index([]byte(s), []byte(old))
	if idx < 0 {
		return s
	}
	return s[:idx] + new + s[idx+len(old):]
}

func writeFileNode(path, content string) *yaml.Node {
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			strNode("path"), strNode(path),
			strNode("permissions"), strNode("0644"),
			strNode("content"), literalNode(content),
		},
	}
}

func literalNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.LiteralStyle, Value: v}
}
