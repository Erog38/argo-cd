package normalizers

import (
	"encoding/json"

	"github.com/argoproj/argo-cd/engine/pkg/utils/diff"
	"github.com/argoproj/argo-cd/pkg/apis/application/v1alpha1"

	jsonpatch "github.com/evanphx/json-patch"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type normalizerPatch struct {
	groupKind schema.GroupKind
	namespace string
	name      string
	patch     jsonpatch.Patch
}

type ignoreNormalizer struct {
	patches []normalizerPatch
}

type overrideIgnoreDiff struct {
	JSONPointers []string `yaml:"jsonPointers"`
}

// NewIgnoreNormalizer creates diff normalizer which removes ignored fields according to given application spec and resource overrides
func NewIgnoreNormalizer(ignore []v1alpha1.ResourceIgnoreDifferences, overrides map[string]v1alpha1.ResourceOverride) (diff.Normalizer, error) {
	for key, override := range overrides {
		group, kind, err := getGroupKindForOverrideKey(key)
		if err != nil {
			log.Warn(err)
		}
		if override.IgnoreDifferences != "" {
			ignoreSettings := overrideIgnoreDiff{}
			err := yaml.Unmarshal([]byte(override.IgnoreDifferences), &ignoreSettings)
			if err != nil {
				return nil, err
			}

			ignore = append(ignore, v1alpha1.ResourceIgnoreDifferences{
				Group:        group,
				Kind:         kind,
				JSONPointers: ignoreSettings.JSONPointers,
			})
		}
	}
	patches := make([]normalizerPatch, 0)
	for i := range ignore {
		for _, path := range ignore[i].JSONPointers {
			patchData, err := json.Marshal([]map[string]string{{"op": "remove", "path": path}})
			if err != nil {
				return nil, err
			}
			patch, err := jsonpatch.DecodePatch(patchData)
			if err != nil {
				return nil, err
			}
			patches = append(patches, normalizerPatch{
				groupKind: schema.GroupKind{Group: ignore[i].Group, Kind: ignore[i].Kind},
				name:      ignore[i].Name,
				namespace: ignore[i].Namespace,
				patch:     patch,
			})
		}

	}
	return &ignoreNormalizer{patches: patches}, nil
}

// Normalize removes fields from supplied resource using json paths from matching items of specified resources ignored differences list
func (n *ignoreNormalizer) Normalize(un *unstructured.Unstructured) error {
	matched := make([]normalizerPatch, 0)
	for _, patch := range n.patches {
		groupKind := un.GroupVersionKind().GroupKind()
		if groupKind == patch.groupKind &&
			(patch.name == "" || patch.name == un.GetName()) &&
			(patch.namespace == "" || patch.namespace == un.GetNamespace()) {

			matched = append(matched, patch)
		}
	}
	if len(matched) == 0 {
		return nil
	}

	docData, err := json.Marshal(un)
	if err != nil {
		return err
	}

	for _, patch := range matched {
		patchedData, err := patch.patch.Apply(docData)
		if err != nil {
			log.Debugf("Failed to apply normalization: %v", err)
			continue
		}
		docData = patchedData
	}

	err = json.Unmarshal(docData, un)
	if err != nil {
		return err
	}
	return nil
}
