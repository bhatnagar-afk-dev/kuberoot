// Package curated arranges resource types into familiar sections, with
// custom resources grouped by API group and everything else under Other.
package curated

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"kuberoot.in/kuberoot/internal/resources"
)

// Section titles that are not in the sections table.
const (
	CustomResources = "Custom Resources"
	Other           = "Other"
)

type section struct {
	title string
	keys  []string // "group/resource", in display order
}

// sections is the curated layout. Types not served by a cluster are skipped.
var sections = []section{
	{"Workloads", []string{
		"/pods", "apps/deployments", "apps/statefulsets", "apps/daemonsets", "apps/replicasets",
		"batch/jobs", "batch/cronjobs",
	}},
	{"Network", []string{
		"/services", "discovery.k8s.io/endpointslices", "networking.k8s.io/ingresses",
		"networking.k8s.io/ingressclasses", "networking.k8s.io/networkpolicies",
		"gateway.networking.k8s.io/gateways", "gateway.networking.k8s.io/httproutes",
	}},
	{"Config", []string{
		"/configmaps", "/secrets", "/resourcequotas", "/limitranges",
		"autoscaling/horizontalpodautoscalers", "policy/poddisruptionbudgets",
		"scheduling.k8s.io/priorityclasses", "node.k8s.io/runtimeclasses",
	}},
	{"Storage", []string{
		"/persistentvolumeclaims", "/persistentvolumes", "storage.k8s.io/storageclasses",
		"storage.k8s.io/volumeattachments", "storage.k8s.io/csidrivers",
	}},
	{"Access", []string{
		"/serviceaccounts", "rbac.authorization.k8s.io/roles", "rbac.authorization.k8s.io/rolebindings",
		"rbac.authorization.k8s.io/clusterroles", "rbac.authorization.k8s.io/clusterrolebindings",
	}},
	{"Cluster", []string{
		"/nodes", "/namespaces", "/events", "apiregistration.k8s.io/apiservices",
		"apiextensions.k8s.io/customresourcedefinitions", "coordination.k8s.io/leases",
		"admissionregistration.k8s.io/mutatingwebhookconfigurations",
		"admissionregistration.k8s.io/validatingwebhookconfigurations",
	}},
}

// Navigator is the curated layout. The zero value is ready to use.
type Navigator struct{}

var _ resources.Navigator = Navigator{}

// Navigation returns the sections in order, then Custom Resources and Other.
// Empty sections are left out.
func (Navigator) Navigation(types []resources.ResourceType) []resources.NavGroup {
	byKey := make(map[string]resources.ResourceType, len(types))
	for _, t := range types {
		byKey[t.Group+"/"+t.Resource] = t
	}

	var groups []resources.NavGroup
	used := map[string]bool{} // titles of curated items
	for _, s := range sections {
		var items []resources.NavItem
		for _, k := range s.keys {
			if t, ok := byKey[k]; ok {
				items = append(items, item(t))
				used[items[len(items)-1].Title] = true
				delete(byKey, k)
			}
		}
		if len(items) > 0 {
			groups = append(groups, resources.NavGroup{Title: s.title, Items: items})
		}
	}

	custom := map[string][]resources.NavItem{}
	var other []resources.NavItem
	for _, t := range byKey {
		it := item(t)
		if builtIn(t.Group) {
			if used[it.Title] {
				it.Title += " (" + t.Group + ")"
			}
			other = append(other, it)
		} else {
			custom[t.Group] = append(custom[t.Group], it)
		}
	}

	if len(custom) > 0 {
		var folders []resources.NavItem
		for _, g := range slices.Sorted(maps.Keys(custom)) {
			items := custom[g]
			slices.SortFunc(items, byTitle)
			folders = append(folders, resources.NavItem{Title: g, Items: items})
		}
		groups = append(groups, resources.NavGroup{Title: CustomResources, Items: folders})
	}
	if len(other) > 0 {
		slices.SortFunc(other, byTitle)
		groups = append(groups, resources.NavGroup{Title: Other, Items: other})
	}
	return groups
}

func item(t resources.ResourceType) resources.NavItem {
	return resources.NavItem{Title: Title(t), Type: &t}
}

// builtIn reports whether group is a Kubernetes API group. Custom resource
// groups must contain a dot, and *.k8s.io groups are reserved for Kubernetes.
func builtIn(group string) bool {
	return !strings.Contains(group, ".") || strings.HasSuffix(group, ".k8s.io")
}

// Title is the display name of a type: its Kind in the server's plural form
// ("Ingress" served as "ingresses" becomes "Ingresses"), or the Kind itself
// when the plural doesn't match it.
func Title(t resources.ResourceType) string {
	kind, res := t.Kind, t.Resource
	lower := strings.ToLower(kind)
	switch {
	case kind == "":
		return res
	case res == lower:
		return kind
	case res == lower+"s" || res == lower+"es":
		return kind + res[len(lower):]
	case strings.HasSuffix(lower, "y") && res == lower[:len(lower)-1]+"ies":
		return kind[:len(kind)-1] + "ies"
	default:
		return kind
	}
}

func byTitle(a, b resources.NavItem) int {
	return cmp.Or(cmp.Compare(a.Title, b.Title), cmp.Compare(a.Type.Group, b.Type.Group))
}
