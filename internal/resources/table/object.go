// Package table lists and watches resources in the server-side Table format
// (the columns `kubectl get` prints) and exposes each table row as a
// runtime.Object, so a standard client-go informer can cache rows.
package table

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"kuberoot.in/kuberoot/internal/resources"
)

// RowObject is one table row: the object's metadata plus its cells.
type RowObject struct {
	metav1.PartialObjectMetadata
	Cells []any
}

// DeepCopyObject implements runtime.Object.
func (r *RowObject) DeepCopyObject() runtime.Object {
	if r == nil {
		return nil
	}
	out := &RowObject{PartialObjectMetadata: *r.PartialObjectMetadata.DeepCopy()} //nolint:staticcheck // explicit: r.DeepCopy() reads as copying the whole row
	if r.Cells != nil {
		out.Cells = make([]any, len(r.Cells))
		for i, c := range r.Cells {
			out.Cells[i] = runtime.DeepCopyJSONValue(c)
		}
	}
	return out
}

// Row converts the object to the type sent to the UI.
func (r *RowObject) Row() resources.Row {
	return resources.Row{
		UID:             string(r.UID),
		Namespace:       r.Namespace,
		Name:            r.Name,
		Created:         r.CreationTimestamp.Time,
		ResourceVersion: r.ResourceVersion,
		Cells:           r.Cells,
	}
}

// RowList is the result of one LIST call.
type RowList struct {
	metav1.TypeMeta
	metav1.ListMeta
	Items []RowObject
}

// DeepCopyObject implements runtime.Object.
func (l *RowList) DeepCopyObject() runtime.Object {
	if l == nil {
		return nil
	}
	out := &RowList{TypeMeta: l.TypeMeta, ListMeta: *l.ListMeta.DeepCopy()} //nolint:staticcheck // explicit, as in RowObject
	if l.Items != nil {
		out.Items = make([]RowObject, len(l.Items))
		for i := range l.Items {
			out.Items[i] = *l.Items[i].DeepCopyObject().(*RowObject)
		}
	}
	return out
}
