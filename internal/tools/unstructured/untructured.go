package unstructured

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools/unstructured/condition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type ObjectRef struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
}

func (o *ObjectRef) String() string {
	return fmt.Sprintf("%s.%s as %s@%s", o.APIVersion, o.Kind, o.Name, o.Namespace)
}

type NotAvailableError struct {
	FailedObjectRef *ObjectRef
	Err             error
}

func (r *NotAvailableError) Error() string {
	if r.FailedObjectRef == nil {
		return fmt.Sprintf("err %v", r.Err)
	}
	return fmt.Sprintf("failedObjectRef %s: err %v", r.FailedObjectRef, r.Err)
}

func IsAvailable(un *unstructured.Unstructured) (bool, error) {
	positives := []string{
		"ready", "complete", "healthy", "active", "able",
	}

	conds := GetConditions(un)
	if len(conds) == 0 {
		return true, nil
	}

	for _, co := range conds {
		if has(positives, string(co.Type)) {
			if string(co.Status) != "True" {
				return false, &NotAvailableError{
					FailedObjectRef: &ObjectRef{
						APIVersion: un.GetAPIVersion(),
						Kind:       un.GetKind(),
						Name:       un.GetName(),
						Namespace:  un.GetNamespace(),
					},
					Err: fmt.Errorf(co.Reason),
				}
			}
		}
	}

	return true, nil
}

func SetCondition(un *unstructured.Unstructured, co metav1.Condition) error {
	conds := GetConditions(un)
	condition.Upsert(&conds, co)

	res, err := encodeStruct(conds)
	if err != nil {
		return err
	}

	return unstructured.SetNestedField(un.Object, res, "status", "conditions")
}

// GetConditions returns the conditions, excluding the `message` field.
func GetConditions(un *unstructured.Unstructured) []metav1.Condition {
	if un == nil {
		return nil
	}
	items, _, _ := unstructured.NestedSlice(un.Object, "status", "conditions")
	x := []metav1.Condition{}
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			return nil
		}
		_, ok = m["type"].(string)
		if !ok {
			return nil
		}
		_, ok = m["status"].(string)
		if !ok {
			return nil
		}
		x = append(x, metav1.Condition{
			Type:   m["type"].(string),
			Status: metav1.ConditionStatus(m["status"].(string)),
		})
	}
	return x
}

func SetFailedObjectRef(un *unstructured.Unstructured, ref *ObjectRef) error {
	return setNestedFieldNoCopy(un, map[string]interface{}{
		"apiVersion": ref.APIVersion,
		"kind":       ref.Kind,
		"name":       ref.Name,
		"namespace":  ref.Namespace,
	}, "status", "failedObjectRef")
}

func ExtractFailedObjectRef(un *unstructured.Unstructured) (*ObjectRef, error) {
	obj, ok, err := unstructured.NestedFieldNoCopy(un.Object, "status", "failedObjectRef")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	dat, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}

	ref := &ObjectRef{}
	err = json.Unmarshal(dat, ref)
	return ref, err
}

func UnsetFailedObjectRef(un *unstructured.Unstructured) {
	removeNestedField(un, "status", "failedObjectRef")
}

func setNestedFieldNoCopy(uns *unstructured.Unstructured, value interface{}, fields ...string) error {
	m := uns.Object

	for i, field := range fields[:len(fields)-1] {
		if val, ok := m[field]; ok {
			if valMap, ok := val.(map[string]interface{}); ok {
				m = valMap
			} else {
				return fmt.Errorf("value cannot be set because %v is not a map[string]interface{}", fields[:i+1])
			}
		} else {
			newVal := make(map[string]interface{})
			m[field] = newVal
			m = newVal
		}
	}
	m[fields[len(fields)-1]] = value
	return nil
}

// removeNestedField removes the nested field from the obj.
func removeNestedField(uns *unstructured.Unstructured, fields ...string) {
	m := uns.Object
	for _, field := range fields[:len(fields)-1] {
		if x, ok := m[field].(map[string]interface{}); ok {
			m = x
		} else {
			return
		}
	}
	delete(m, fields[len(fields)-1])
}

// has checks if a string is present in a slice
func has(s []string, str string) bool {
	for _, v := range s {
		if strings.Contains(strings.ToLower(str), v) {
			return true
		}
	}

	return false
}

// Converts a struct to a map while maintaining the json alias as keys
func encodeStruct(obj interface{}) (res interface{}, err error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return
	}

	err = json.Unmarshal(data, &res)
	return
}
