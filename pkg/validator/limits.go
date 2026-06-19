package validator

import (
	"encoding/json"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
)

// ValidateResourceLimits decodes a Deployment from raw admission bytes and
// checks that every container declares both CPU and memory limits.
// It returns a list of human-readable violation messages. An empty slice
// means the Deployment passed.
func ValidateResourceLimits(raw []byte) ([]string, error) {
	var deployment appsv1.Deployment
	if err := json.Unmarshal(raw, &deployment); err != nil {
		return nil, fmt.Errorf("failed to decode Deployment: %w", err)
	}

	var violations []string

	containers := deployment.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		violations = append(violations, "deployment has no containers defined")
		return violations, nil
	}

	for _, c := range containers {
		limits := c.Resources.Limits

		if limits == nil {
			violations = append(violations,
				fmt.Sprintf("container %q has no resource limits defined", c.Name))
			continue
		}

		if _, ok := limits["cpu"]; !ok {
			violations = append(violations,
				fmt.Sprintf("container %q is missing a CPU limit", c.Name))
		}
		if _, ok := limits["memory"]; !ok {
			violations = append(violations,
				fmt.Sprintf("container %q is missing a memory limit", c.Name))
		}
	}

	return violations, nil
}
