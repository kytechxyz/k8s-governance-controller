package validator

import (
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type metaOnly struct {
	Metadata metav1.ObjectMeta `json:"metadata"`
}

var requiredLabels = []string{
	"cost-center",
	"team",
	"environment",
}

func ValidateRequiredLabels(raw []byte) ([]string, error) {
	var obj metaOnly
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("failed to decode object metadata: %w", err)
	}

	var violations []string
	labels := obj.Metadata.Labels

	for _, key := range requiredLabels {
		value, ok := labels[key]
		if !ok {
			violations = append(violations,
				fmt.Sprintf("missing required label %q", key))
			continue
		}
		if value == "" {
			violations = append(violations,
				fmt.Sprintf("required label %q is present but empty", key))
		}
	}

	return violations, nil
}
