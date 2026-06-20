package validator

import (
	"testing"
)

type labelsTestCase struct {
	name           string
	objectJSON     string
	wantViolations int
}

func TestValidateRequiredLabels(t *testing.T) {
	cases := []labelsTestCase{
		{
			name: "all labels present and non-empty",
			objectJSON: `{
				"metadata": {
					"labels": {
						"cost-center": "eng-platform",
						"team": "infra",
						"environment": "production"
					}
				}
			}`,
			wantViolations: 0,
		},
		{
			name: "one label missing entirely",
			objectJSON: `{
				"metadata": {
					"labels": {
						"team": "infra",
						"environment": "production"
					}
				}
			}`,
			wantViolations: 1,
		},
		{
			name: "one label present but empty",
			objectJSON: `{
				"metadata": {
					"labels": {
						"cost-center": "eng-platform",
						"team": "",
						"environment": "production"
					}
				}
			}`,
			wantViolations: 1,
		},
		{
			name: "two problems at once - one missing, one empty",
			objectJSON: `{
				"metadata": {
					"labels": {
						"team": "",
						"environment": "production"
					}
				}
			}`,
			wantViolations: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateRequiredLabels([]byte(tc.objectJSON))
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
