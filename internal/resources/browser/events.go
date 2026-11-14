package browser

import (
	"maps"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/fields"

	"kuberoot.in/kuberoot/internal/resources"
	"kuberoot.in/kuberoot/internal/resources/view"
)

// EventsType is core/v1 events, which `kubectl describe` reads.
var EventsType = resources.ResourceType{Version: "v1", Resource: "events", Kind: "Event", Namespaced: true}

// EventsRequest returns the view of the events about ref, selected the way
// `kubectl describe` selects them: by the object's name, namespace and UID.
// A Node's events are selected by kind instead of UID, because the kubelet
// records them with the node's name as UID.
func EventsRequest(viewID string, ref resources.ObjectRef) view.Request {
	sel := fields.Set{
		"involvedObject.name":      ref.Name,
		"involvedObject.namespace": ref.Namespace,
	}
	switch {
	case ref.Type.Group == "" && ref.Type.Kind == "Node":
		sel["involvedObject.kind"] = "Node"
	case ref.UID != "":
		sel["involvedObject.uid"] = ref.UID
	}
	var namespaces []string // all, for cluster-scoped objects
	if ref.Type.Namespaced {
		namespaces = []string{ref.Namespace}
	}
	return view.Request{
		ViewID:        viewID,
		Type:          EventsType,
		Namespaces:    namespaces,
		FieldSelector: selector(sel),
	}
}

// selector formats a field selector with sorted terms, so the same object
// always gets the same informer key.
func selector(set fields.Set) string {
	terms := make([]string, 0, len(set))
	for _, k := range slices.Sorted(maps.Keys(set)) {
		terms = append(terms, k+"="+fields.EscapeValue(set[k]))
	}
	return strings.Join(terms, ",")
}
