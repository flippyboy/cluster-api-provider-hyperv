// Package cloudinit builds a NoCloud ISO and injects the Hyper-V provider ID
// into kubeadm cloud-config.
package cloudinit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// InvalidBootstrapError means user-data is not cloud-config that contains a
// kubeadm InitConfiguration or JoinConfiguration.
type InvalidBootstrapError struct {
	Detail string
}

func (e *InvalidBootstrapError) Error() string {
	return "bootstrap data is not kubeadm cloud-config: " + e.Detail
}

// IsInvalidBootstrap reports whether err is an InvalidBootstrapError.
func IsInvalidBootstrap(err error) bool {
	var target *InvalidBootstrapError
	return errors.As(err, &target)
}

// InjectProviderID sets kubeletExtraArgs.provider-id inside every kubeadm
// InitConfiguration or JoinConfiguration embedded in write_files[].content.
// v1beta3 stores kubeletExtraArgs as a map. v1beta4 stores a list of
// name/value pairs. An existing provider-id is replaced.
func InjectProviderID(userData []byte, providerID string) ([]byte, error) {
	if strings.TrimSpace(providerID) == "" {
		return nil, fmt.Errorf("provider id is empty")
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
	patched := false
	for _, doc := range docs {
		changed, err := patchCloudConfig(doc, providerID)
		if err != nil {
			return nil, err
		}
		patched = patched || changed
	}
	if !patched {
		return nil, &InvalidBootstrapError{Detail: "no InitConfiguration or JoinConfiguration in write_files"}
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

func patchCloudConfig(doc *yaml.Node, providerID string) (bool, error) {
	root := mappingOf(doc)
	if root == nil {
		return false, nil
	}
	files := mapGet(root, "write_files")
	if files == nil || files.Kind != yaml.SequenceNode {
		return false, nil
	}
	patched := false
	for _, file := range files.Content {
		item := mappingOf(file)
		if item == nil {
			continue
		}
		content := mapGet(item, "content")
		if content == nil || content.Kind != yaml.ScalarNode || strings.TrimSpace(content.Value) == "" {
			continue
		}
		updated, changed, err := patchKubeadmYAML(content.Value, providerID)
		if err != nil {
			return false, err
		}
		if !changed {
			continue
		}
		content.Kind = yaml.ScalarNode
		content.Tag = "!!str"
		content.Style = yaml.LiteralStyle
		content.Value = updated
		patched = true
	}
	return patched, nil
}

func patchKubeadmYAML(text, providerID string) (string, bool, error) {
	dec := yaml.NewDecoder(strings.NewReader(text))
	var docs []*yaml.Node
	changed := false
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", false, nil
		}
		ok, err := patchKubeadmDoc(&doc, providerID)
		if err != nil {
			return "", false, err
		}
		changed = changed || ok
		docs = append(docs, &doc)
	}
	if !changed {
		return "", false, nil
	}
	out, err := encodeDocs(docs)
	if err != nil {
		return "", false, err
	}
	return string(out), true, nil
}

func patchKubeadmDoc(doc *yaml.Node, providerID string) (bool, error) {
	root := mappingOf(doc)
	if root == nil {
		return false, nil
	}
	kind := mapScalar(root, "kind")
	if kind != "InitConfiguration" && kind != "JoinConfiguration" {
		return false, nil
	}
	v1beta4 := strings.Contains(mapScalar(root, "apiVersion"), "v1beta4")
	if err := patchNodeRegistration(root, providerID, v1beta4); err != nil {
		return false, err
	}
	return true, nil
}

func patchNodeRegistration(root *yaml.Node, providerID string, v1beta4 bool) error {
	nr := mapGet(root, "nodeRegistration")
	if nr == nil {
		nr = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		mapSet(root, "nodeRegistration", nr)
	}
	if nr.Kind != yaml.MappingNode {
		return fmt.Errorf("nodeRegistration is not a mapping")
	}
	args := mapGet(nr, "kubeletExtraArgs")
	if args == nil {
		if v1beta4 {
			args = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		} else {
			args = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		mapSet(nr, "kubeletExtraArgs", args)
	}
	switch args.Kind {
	case yaml.SequenceNode:
		return patchArgList(args, providerID)
	case yaml.MappingNode:
		mapSet(args, "provider-id", strNode(providerID))
		return nil
	default:
		return fmt.Errorf("kubeletExtraArgs is not a map or a list")
	}
}

func patchArgList(seq *yaml.Node, providerID string) error {
	for _, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if mapScalar(item, "name") == "provider-id" {
			mapSet(item, "value", strNode(providerID))
			return nil
		}
	}
	seq.Content = append(seq.Content, &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			strNode("name"), strNode("provider-id"),
			strNode("value"), strNode(providerID),
		},
	})
	return nil
}

func mappingOf(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func mapSet(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, strNode(key), val)
}

func mapScalar(m *yaml.Node, key string) string {
	n := mapGet(m, key)
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

func strNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

func encodeDocs(docs []*yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	for _, doc := range docs {
		if err := enc.Encode(doc); err != nil {
			_ = enc.Close()
			return nil, fmt.Errorf("encode yaml: %w", err)
		}
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode yaml: %w", err)
	}
	return buf.Bytes(), nil
}
