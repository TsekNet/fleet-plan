// Package envmerge merges a base YAML config with an environment overlay,
// equivalent to: yq eval-all '. as $item ireduce ({}; . *+ $item)' base.yml env.yml
package merge

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// MergeFiles reads base and overlay YAML files and deep-merges them,
// writing the result to outPath. Overlay values take precedence.
func MergeFiles(basePath, overlayPath, outPath string) error {
	baseData, err := os.ReadFile(basePath)
	if err != nil {
		return fmt.Errorf("reading base %q: %w", basePath, err)
	}
	overlayData, err := os.ReadFile(overlayPath)
	if err != nil {
		return fmt.Errorf("reading env %q: %w", overlayPath, err)
	}

	merged, err := merge(baseData, overlayData)
	if err != nil {
		return err
	}

	out, err := yaml.Marshal(merged)
	if err != nil {
		return fmt.Errorf("marshalling merged config: %w", err)
	}
	if err := os.WriteFile(outPath, out, 0o600); err != nil {
		return fmt.Errorf("writing merged config to %q: %w", outPath, err)
	}
	return nil
}

// merge deep-merges overlay into base. Maps are recursively merged;
// all other types are overwritten by the overlay value.
func merge(baseYAML, overlayYAML []byte) (map[string]any, error) {
	var base map[string]any
	if err := yaml.Unmarshal(baseYAML, &base); err != nil {
		return nil, fmt.Errorf("parsing base YAML: %w", err)
	}
	var overlay map[string]any
	if err := yaml.Unmarshal(overlayYAML, &overlay); err != nil {
		return nil, fmt.Errorf("parsing overlay YAML: %w", err)
	}
	if base == nil {
		base = map[string]any{}
	}
	deepMerge(base, overlay)
	return base, nil
}

// deepMerge merges src into dst in place. Maps are merged recursively;
// all other values in src overwrite dst.
func deepMerge(dst, src map[string]any) {
	for k, sv := range src {
		dv, exists := dst[k]
		if !exists {
			dst[k] = sv
			continue
		}
		dstMap, dstIsMap := dv.(map[string]any)
		srcMap, srcIsMap := sv.(map[string]any)
		if dstIsMap && srcIsMap {
			deepMerge(dstMap, srcMap)
		} else {
			dst[k] = sv
		}
	}
}
