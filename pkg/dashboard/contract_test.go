package dashboard

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/JyotinderSingh/task-queue/pkg/model"
	"github.com/JyotinderSingh/task-queue/pkg/store"
)

// TestContractMatchesGoStructs pins the browser/API boundary.
//
// The dashboard reads the JSON these structs emit, and neither language would
// notice a renamed field: it would surface as `undefined` in a browser rather
// than as a build failure, and the screen would render an empty cell as if that
// were the truth. web/contract.json is the shared definition both ends are held
// to, and this is what holds the Go end to it.
//
// A field added or renamed on purpose is meant to fail here. Update
// contract.json, and the screen that reads the field, in the same change.
func TestContractMatchesGoStructs(t *testing.T) {
	declared := readContract(t)

	for name, value := range map[string]interface{}{
		"Agent":         model.Agent{},
		"AgentOverview": store.AgentOverview{},
		"Schedule":      model.Schedule{},
		"Run":           model.Run{},
		"Step":          store.Step{},
		"MemoryRecord":  store.MemoryRecord{},
		"Secret":        store.Secret{},
	} {
		expected, ok := declared[name]
		if !ok {
			t.Errorf("contract.json does not describe %s", name)
			continue
		}

		actual := jsonFields(reflect.TypeOf(value))
		if !reflect.DeepEqual(sorted(expected), sorted(actual)) {
			t.Errorf("%s has drifted from contract.json:\n  contract: %v\n  Go:       %v",
				name, sorted(expected), sorted(actual))
		}
	}
}

// jsonFields returns the field names a struct marshals to, following embedded
// structs the way encoding/json does - inlining their fields rather than
// nesting them.
func jsonFields(structType reflect.Type) []string {
	names := []string{}
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		tag := field.Tag.Get("json")

		if field.Anonymous && tag == "" {
			names = append(names, jsonFields(field.Type)...)
			continue
		}
		if tag == "" || tag == "-" {
			continue
		}
		for j, char := range tag {
			if char == ',' {
				tag = tag[:j]
				break
			}
		}
		names = append(names, tag)
	}
	return names
}

func readContract(t *testing.T) map[string][]string {
	t.Helper()

	raw, err := os.ReadFile("web/contract.json")
	if err != nil {
		t.Fatalf("reading the shared type definition: %v", err)
	}

	var contract struct {
		Types map[string][]string `json:"types"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatalf("decoding the shared type definition: %v", err)
	}
	return contract.Types
}

func sorted(values []string) []string {
	copied := append([]string(nil), values...)
	sort.Strings(copied)
	return copied
}
