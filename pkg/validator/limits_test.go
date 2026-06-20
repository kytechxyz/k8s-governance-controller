package validator

import (
	"testing"
)

type limitsTestCase struct {
	name           string
	deploymentJSON string
	wantViolations int
}
func TestValidateResourceLimits(t *testing.T) {
	cases := []limitsTestCase{
		{
			name: "compliant deployment, zero violations",
			deploymentJSON: `{
				"spec": {
					"template": {
						"spec": {
							"containers": [
								{"name": "web", "resources": {"limits": {"cpu": "100m", "memory": "128Mi"}}}
							]
						}
					}
				}
			}`,
			wantViolations: 0,
		},
		{
			name: "single container, no limits block at all",
			deploymentJSON: `{
				"spec": {
					"template": {
						"spec": {
							"containers": [
								{"name": "web"}
							]
						}
					}
				}
			}`,
			wantViolations: 1,
		},
		{
			name: "single container, missing CPU only",
			deploymentJSON: `{
				"spec": {
					"template": {
						"spec": {
							"containers": [
								{"name": "web", "resources": {"limits": {"memory": "128Mi"}}}
							]
						}
					}
				}
			}`,
			wantViolations: 1,
		},
		{
			name: "single container, missing memory only",
			deploymentJSON: `{
				"spec": {
					"template": {
						"spec": {
							"containers": [
								{"name": "web", "resources": {"limits": {"cpu": "100m"}}}
							]
						}
					}
				}
			}`,
			wantViolations: 1,
		},
		{
			name: "multi-container, one healthy one broken",
			deploymentJSON: `{
				"spec": {
					"template": {
						"spec": {
							"containers": [
								{"name": "web", "resources": {"limits": {"cpu": "100m", "memory": "128Mi"}}},
								{"name": "sidecar", "resources": {"limits": {"cpu": "50m"}}}
							]
						}
					}
				}
			}`,
			wantViolations: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateResourceLimits([]byte(tc.deploymentJSON))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tc.wantViolations {
				t.Errorf("got %d violations, want %d\nviolations: %v",
					len(got), tc.wantViolations, got)
			}
		})
	}
}