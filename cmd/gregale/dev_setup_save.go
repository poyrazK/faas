package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"gopkg.in/yaml.v3"
)

type devSetupSavedConfig struct {
	Path    string         `json:"path"`
	DryRun  bool           `json:"dry_run"`
	Changed bool           `json:"changed"`
	Values  map[string]any `json:"dev"`
}

func saveDevSetup(receipt devSetupReceipt, postgres bool, region string, dryRun bool) (*devSetupSavedConfig, error) {
	env, err := portableDevFile(receipt.Path, receipt.EnvFile)
	if err != nil {
		return nil, err
	}
	services, err := portableDevFile(receipt.Path, receipt.ServiceOverridesFile)
	if err != nil {
		return nil, err
	}
	values := map[string]any{"ttl": receipt.TTL, "postgres": postgres, "postgres_region": region, "env_file": env, "service_override_file": services, "buckets": receipt.Buckets}
	edit, exists, err := devSetupManifestEdit(receipt.Path, values)
	if err != nil {
		return nil, err
	}
	result := &devSetupSavedConfig{Path: edit.path, DryRun: dryRun, Changed: !bytes.Equal(edit.before, edit.after), Values: values}
	if dryRun || !result.Changed {
		return result, nil
	}
	if exists {
		err = applyStartFileEdit(receipt.Path, edit)
	} else {
		err = createDevSetupManifest(edit)
	}
	if err != nil {
		return nil, fmt.Errorf("save developer defaults: %w", err)
	}
	return result, nil
}

func portableDevFile(source, path string) (string, error) {
	if path == "" {
		return "", nil
	}
	rel, err := filepath.Rel(source, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", errors.New("saved setup files must be inside the source directory; move the env or service override file there before using --save")
	}
	// Resolve symlinks as well, so a portable-looking path cannot depend on
	// another checkout or the original developer's home directory.
	if _, _, err := readStartEditableFile(source, path); err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func devSetupManifestEdit(source string, values map[string]any) (startFileEdit, bool, error) {
	edit := startFileEdit{path: filepath.Join(source, "gregale.yaml"), mode: 0644}
	exists := false
	for _, filename := range []string{"gregale.yaml", "gregale.yml", "gregale.toml"} {
		path := filepath.Join(source, filename)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return edit, false, err
		}
		if filename == "gregale.toml" {
			return edit, false, errors.New("--save supports gregale.yaml or gregale.yml; migrate gregale.toml explicitly before saving developer defaults")
		}
		var err error
		edit.path = path
		edit.before, edit.mode, err = readStartEditableFile(source, path)
		if err != nil {
			return edit, false, err
		}
		exists = true
		break
	}
	after, err := mergeDevSetupYAML(edit.before, values)
	edit.after = after
	return edit, exists, err
}

func mergeDevSetupYAML(before []byte, values map[string]any) ([]byte, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(before, &document); err != nil {
		return nil, fmt.Errorf("read manifest YAML: %w", err)
	}
	if len(document.Content) == 0 {
		document = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("manifest must be a YAML mapping")
	}
	dev := devSetupMappingValue(root, "dev")
	if dev == nil {
		dev = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "dev"}, dev)
	}
	if dev.Kind != yaml.MappingNode {
		return nil, errors.New("manifest dev must be a direct YAML mapping to save setup choices")
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		var value yaml.Node
		if err := value.Encode(values[key]); err != nil {
			return nil, err
		}
		if old := devSetupMappingValue(dev, key); old != nil {
			value.HeadComment, value.LineComment, value.FootComment = old.HeadComment, old.LineComment, old.FootComment
			*old = value
		} else {
			dev.Content = append(dev.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &value)
		}
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	manifest, err := gregalemanifest.ParseBytes(output.Bytes())
	if err != nil {
		return nil, fmt.Errorf("validate saved setup: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("validate saved setup: %w", err)
	}
	return output.Bytes(), nil
}

func devSetupMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func createDevSetupManifest(edit startFileEdit) error {
	file, err := os.CreateTemp(filepath.Dir(edit.path), ".gregale-setup-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if err := file.Chmod(edit.mode); err != nil {
		return err
	}
	if _, err := file.Write(edit.after); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	// Linking the completed file refuses to overwrite a file created since
	// the preview. The temporary name is removed by the deferred cleanup.
	return os.Link(file.Name(), edit.path)
}
