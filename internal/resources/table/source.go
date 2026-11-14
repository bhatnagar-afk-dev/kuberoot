package table

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"kuberoot.in/kuberoot/internal/resources"
)

const (
	acceptTable        = "application/json;as=Table;v=v1;g=meta.k8s.io"
	acceptMetadataList = "application/json;as=PartialObjectMetadataList;v=v1;g=meta.k8s.io"
	acceptMetadata     = "application/json;as=PartialObjectMetadata;v=v1;g=meta.k8s.io"
)

// fallbackColumns are used when a server cannot return the Table format.
var fallbackColumns = []resources.Column{
	{Name: "Name", Type: "string", Format: "name"},
	{Name: "Age", Type: "string"},
}

// Source lists and watches one resource type in one namespace ("" for all
// namespaces or cluster-scoped types) as table rows, optionally limited by a
// field selector. It implements
// cache.ListerWatcherWithContext. Column definitions are taken from the most
// recent LIST.
type Source struct {
	client    rest.Interface
	gvr       schema.GroupVersionResource
	namespace string
	selector  string // field selector; "" selects everything

	mu           sync.Mutex
	columns      []resources.Column
	metadataOnly bool // server rejected the Table format; use fallbackColumns
}

// NewSource returns a Source for gvr in namespace using cfg. fieldSelector
// (e.g. "involvedObject.uid=...") limits the rows; "" selects all.
func NewSource(cfg *rest.Config, gvr schema.GroupVersionResource, namespace, fieldSelector string) (*Source, error) {
	c := rest.CopyConfig(cfg)
	c.NegotiatedSerializer = scheme.Codecs.WithoutConversion()
	client, err := rest.UnversionedRESTClientFor(c)
	if err != nil {
		return nil, fmt.Errorf("rest client: %w", err)
	}
	return &Source{client: client, gvr: gvr, namespace: namespace, selector: fieldSelector}, nil
}

// Columns returns the column definitions from the last LIST, or nil before
// the first LIST.
func (s *Source) Columns() []resources.Column {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.columns)
}

// IsWatchListSemanticsUnSupported tells client-go's reflector to use plain
// LIST + WATCH. Streaming lists (WatchList) don't support the Table format.
func (s *Source) IsWatchListSemanticsUnSupported() bool { return true }

// List implements cache.Lister, which informer constructors still require.
// The reflector calls ListWithContext instead.
func (s *Source) List(opts metav1.ListOptions) (runtime.Object, error) {
	return s.ListWithContext(context.Background(), opts)
}

// Watch implements cache.Watcher; see List.
func (s *Source) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	return s.WatchWithContext(context.Background(), opts)
}

// ListWithContext implements cache.ListerWithContext.
func (s *Source) ListWithContext(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
	if !s.isMetadataOnly() {
		raw, err := s.request(opts, acceptTable).Do(ctx).Raw()
		switch {
		case apierrors.IsNotAcceptable(err) || apierrors.IsUnsupportedMediaType(err):
			s.useMetadataOnly()
		case err != nil:
			return nil, err
		default:
			var t metav1.Table
			if err := json.Unmarshal(raw, &t); err != nil {
				return nil, fmt.Errorf("decode table: %w", err)
			}
			if t.Kind == "Table" {
				items, err := tableRows(&t)
				if err != nil {
					return nil, err
				}
				if len(t.ColumnDefinitions) > 0 {
					s.setColumns(columns(t.ColumnDefinitions))
				}
				return &RowList{ListMeta: t.ListMeta, Items: items}, nil
			}
			// The server ignored the Table request (some aggregated APIs).
			s.useMetadataOnly()
		}
	}

	raw, err := s.request(opts, acceptMetadataList).Do(ctx).Raw()
	if err != nil {
		return nil, err
	}
	var l metav1.PartialObjectMetadataList
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, fmt.Errorf("decode metadata list: %w", err)
	}
	items := make([]RowObject, len(l.Items))
	for i := range l.Items {
		items[i] = metadataRow(l.Items[i])
	}
	return &RowList{ListMeta: l.ListMeta, Items: items}, nil
}

// WatchWithContext implements cache.WatcherWithContext.
func (s *Source) WatchWithContext(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	opts.AllowWatchBookmarks = false // bookmarks carry no rows; we don't need them
	metadataOnly := s.isMetadataOnly()
	accept := acceptTable
	if metadataOnly {
		accept = acceptMetadata
	}
	stream, err := s.request(opts, accept).Stream(ctx)
	if err != nil {
		return nil, err
	}
	d := &decoder{r: stream, dec: json.NewDecoder(stream), metadataOnly: metadataOnly}
	return watch.NewStreamWatcher(d, apierrors.NewClientErrorReporter(http.StatusInternalServerError, "GET", "ClientWatchDecoding")), nil
}

func (s *Source) request(opts metav1.ListOptions, accept string) *rest.Request {
	path := []string{"/api", s.gvr.Version}
	if s.gvr.Group != "" {
		path = []string{"/apis", s.gvr.Group, s.gvr.Version}
	}
	if s.namespace != "" {
		path = append(path, "namespaces", s.namespace)
	}
	path = append(path, s.gvr.Resource)
	opts.FieldSelector = s.selector
	return s.client.Get().
		AbsPath(path...).
		SpecificallyVersionedParams(&opts, metav1.ParameterCodec, metav1.SchemeGroupVersion).
		Param("includeObject", string(metav1.IncludeMetadata)).
		SetHeader("Accept", accept)
}

func (s *Source) isMetadataOnly() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.metadataOnly
}

func (s *Source) useMetadataOnly() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metadataOnly = true
	s.columns = slices.Clone(fallbackColumns)
}

func (s *Source) setColumns(c []resources.Column) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.columns = c
}

func columns(defs []metav1.TableColumnDefinition) []resources.Column {
	out := make([]resources.Column, len(defs))
	for i, d := range defs {
		out[i] = resources.Column{Name: d.Name, Type: d.Type, Format: d.Format, Priority: int(d.Priority)}
	}
	return out
}

// tableRows converts a Table's rows. Each row must carry its object's
// metadata (requested with includeObject=Metadata).
func tableRows(t *metav1.Table) ([]RowObject, error) {
	out := make([]RowObject, len(t.Rows))
	for i, r := range t.Rows {
		if len(r.Object.Raw) == 0 {
			return nil, fmt.Errorf("table row %d has no object metadata", i)
		}
		if err := json.Unmarshal(r.Object.Raw, &out[i].PartialObjectMetadata); err != nil {
			return nil, fmt.Errorf("decode row %d metadata: %w", i, err)
		}
		out[i].Cells = r.Cells
	}
	return out, nil
}

func metadataRow(m metav1.PartialObjectMetadata) RowObject {
	age := "<unknown>"
	if !m.CreationTimestamp.IsZero() {
		age = duration.HumanDuration(time.Since(m.CreationTimestamp.Time))
	}
	return RowObject{PartialObjectMetadata: m, Cells: []any{m.Name, age}}
}

// decoder turns a watch stream of Table (or PartialObjectMetadata) events
// into one event per row.
type decoder struct {
	r            io.ReadCloser
	dec          *json.Decoder
	metadataOnly bool
	pending      []watch.Event
}

func (d *decoder) Decode() (watch.EventType, runtime.Object, error) {
	for len(d.pending) == 0 {
		var ev metav1.WatchEvent
		if err := d.dec.Decode(&ev); err != nil {
			return "", nil, err
		}
		typ := watch.EventType(ev.Type)
		switch typ {
		case watch.Error:
			st := &metav1.Status{}
			if err := json.Unmarshal(ev.Object.Raw, st); err != nil {
				return "", nil, fmt.Errorf("decode watch error: %w", err)
			}
			return watch.Error, st, nil
		case watch.Added, watch.Modified, watch.Deleted:
		default:
			continue // bookmarks and unknown event types
		}
		rows, err := d.rows(ev.Object.Raw)
		if err != nil {
			return "", nil, err
		}
		for i := range rows {
			d.pending = append(d.pending, watch.Event{Type: typ, Object: &rows[i]})
		}
	}
	e := d.pending[0]
	d.pending = d.pending[1:]
	return e.Type, e.Object, nil
}

func (d *decoder) rows(raw []byte) ([]RowObject, error) {
	if d.metadataOnly {
		var m metav1.PartialObjectMetadata
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("decode watch metadata: %w", err)
		}
		return []RowObject{metadataRow(m)}, nil
	}
	var t metav1.Table
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("decode watch table: %w", err)
	}
	return tableRows(&t)
}

func (d *decoder) Close() { _ = d.r.Close() }
