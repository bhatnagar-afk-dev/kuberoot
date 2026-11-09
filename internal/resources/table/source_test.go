package table

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

var podsGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

const podColumns = `"columnDefinitions":[
	{"name":"Name","type":"string","format":"name","priority":0},
	{"name":"Ready","type":"string","format":"","priority":0},
	{"name":"IP","type":"string","format":"","priority":1}]`

func podRow(name, uid, rv, ready string) string {
	return fmt.Sprintf(`{"cells":[%q,%q,"10.0.0.1"],"object":{"kind":"PartialObjectMetadata","apiVersion":"meta.k8s.io/v1",
		"metadata":{"name":%q,"namespace":"default","uid":%q,"resourceVersion":%q,"creationTimestamp":"2026-11-01T00:00:00Z"}}}`,
		name, ready, name, uid, rv)
}

func tableJSON(rv string, withColumns bool, rows ...string) string {
	cols := ""
	if withColumns {
		cols = podColumns + ","
	}
	return fmt.Sprintf(`{"kind":"Table","apiVersion":"meta.k8s.io/v1","metadata":{"resourceVersion":%q},%s"rows":[%s]}`,
		rv, cols, strings.Join(rows, ","))
}

func watchEvent(typ, object string) string {
	return fmt.Sprintf(`{"type":%q,"object":%s}`, typ, object) + "\n"
}

func statusJSON(code int, reason string) string {
	return fmt.Sprintf(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":%q,"code":%d}`, reason, code)
}

// fakeServer records requests and serves canned responses.
type fakeServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*http.Request
	handle   func(w http.ResponseWriter, r *http.Request)
}

func newFakeServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) *fakeServer {
	t.Helper()
	f := &fakeServer{handle: handle}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		f.handle(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeServer) lastRequest() *http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func newTestSource(t *testing.T, srv *fakeServer, gvr schema.GroupVersionResource, ns string) *Source {
	t.Helper()
	s, err := NewSource(&rest.Config{Host: srv.URL}, gvr, ns)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestListTable(t *testing.T) {
	srv := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, tableJSON("10", true, podRow("a", "uid-a", "5", "1/1"), podRow("b", "uid-b", "6", "0/1")))
	})
	s := newTestSource(t, srv, podsGVR, "default")

	if s.Columns() != nil {
		t.Error("columns known before first list")
	}
	obj, err := s.ListWithContext(context.Background(), metav1.ListOptions{Limit: 500, ResourceVersion: "0"})
	if err != nil {
		t.Fatal(err)
	}
	r := srv.lastRequest()
	if r.URL.Path != "/api/v1/namespaces/default/pods" {
		t.Errorf("path = %s", r.URL.Path)
	}
	if got := r.Header.Get("Accept"); got != acceptTable {
		t.Errorf("Accept = %q", got)
	}
	q := r.URL.Query()
	if q.Get("includeObject") != "Metadata" || q.Get("limit") != "500" || q.Get("resourceVersion") != "0" {
		t.Errorf("query = %v", q)
	}

	l := obj.(*RowList)
	if l.ResourceVersion != "10" || len(l.Items) != 2 {
		t.Fatalf("list = rv %q, %d items", l.ResourceVersion, len(l.Items))
	}
	row := l.Items[0].Row()
	if row.Name != "a" || row.Namespace != "default" || row.UID != "uid-a" || row.ResourceVersion != "5" ||
		row.Created.IsZero() || len(row.Cells) != 3 || row.Cells[1] != "1/1" {
		t.Errorf("row = %+v", row)
	}
	cols := s.Columns()
	if len(cols) != 3 || cols[0].Name != "Name" || cols[0].Format != "name" || cols[2].Priority != 1 {
		t.Errorf("columns = %+v", cols)
	}
}

func TestPaths(t *testing.T) {
	cases := []struct {
		gvr  schema.GroupVersionResource
		ns   string
		want string
	}{
		{podsGVR, "", "/api/v1/pods"},
		{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, "web", "/apis/apps/v1/namespaces/web/deployments"},
		{schema.GroupVersionResource{Group: "example.com", Version: "v1alpha1", Resource: "widgets"}, "", "/apis/example.com/v1alpha1/widgets"},
	}
	for _, c := range cases {
		srv := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, tableJSON("1", true)) })
		if _, err := newTestSource(t, srv, c.gvr, c.ns).ListWithContext(context.Background(), metav1.ListOptions{}); err != nil {
			t.Fatal(err)
		}
		if got := srv.lastRequest().URL.Path; got != c.want {
			t.Errorf("%v in %q: path = %s, want %s", c.gvr, c.ns, got, c.want)
		}
	}
}

func TestServerPathPrefix(t *testing.T) {
	srv := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, tableJSON("1", true)) })
	// Proxied clusters (e.g. Rancher) put the API under a path prefix.
	s, err := NewSource(&rest.Config{Host: srv.URL + "/k8s/clusters/c-1"}, podsGVR, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListWithContext(context.Background(), metav1.ListOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := srv.lastRequest().URL.Path; got != "/k8s/clusters/c-1/api/v1/pods" {
		t.Errorf("path = %s", got)
	}
}

func TestListErrors(t *testing.T) {
	srv := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, statusJSON(403, "Forbidden"))
	})
	_, err := newTestSource(t, srv, podsGVR, "kube-system").ListWithContext(context.Background(), metav1.ListOptions{})
	if !apierrors.IsForbidden(err) {
		t.Errorf("err = %v, want Forbidden", err)
	}
}

func TestListRowWithoutMetadata(t *testing.T) {
	srv := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"kind":"Table","apiVersion":"meta.k8s.io/v1","metadata":{},"rows":[{"cells":["x"]}]}`)
	})
	if _, err := newTestSource(t, srv, podsGVR, "").ListWithContext(context.Background(), metav1.ListOptions{}); err == nil {
		t.Error("want an error for a row without object metadata")
	}
}

func metadataJSON(name, uid, rv string) string {
	return fmt.Sprintf(`{"kind":"PartialObjectMetadata","apiVersion":"meta.k8s.io/v1","metadata":{"name":%q,"uid":%q,"resourceVersion":%q,"creationTimestamp":"2026-11-01T00:00:00Z"}}`, name, uid, rv)
}

func TestMetadataFallback(t *testing.T) {
	for _, mode := range []string{"406", "ignored"} {
		t.Run(mode, func(t *testing.T) {
			srv := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
				accept := r.Header.Get("Accept")
				switch {
				case accept == acceptTable && mode == "406":
					w.WriteHeader(http.StatusNotAcceptable)
					fmt.Fprint(w, statusJSON(406, "NotAcceptable"))
				case accept == acceptTable: // server returns its normal list
					fmt.Fprintf(w, `{"kind":"NodeMetricsList","apiVersion":"metrics.k8s.io/v1beta1","metadata":{},"items":[%s]}`, metadataJSON("n1", "u1", "1"))
				case accept == acceptMetadataList:
					fmt.Fprintf(w, `{"kind":"PartialObjectMetadataList","apiVersion":"meta.k8s.io/v1","metadata":{"resourceVersion":"7"},"items":[%s]}`, metadataJSON("n1", "u1", "1"))
				case accept == acceptMetadata && r.URL.Query().Get("watch") == "true":
					fmt.Fprint(w, watchEvent("ADDED", metadataJSON("n2", "u2", "8")))
				default:
					t.Errorf("unexpected Accept %q", accept)
				}
			})
			s := newTestSource(t, srv, schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "nodes"}, "")
			obj, err := s.ListWithContext(context.Background(), metav1.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			l := obj.(*RowList)
			if l.ResourceVersion != "7" || len(l.Items) != 1 || l.Items[0].Name != "n1" || l.Items[0].Cells[0] != "n1" {
				t.Fatalf("list = %+v", l)
			}
			if cols := s.Columns(); len(cols) != 2 || cols[0].Name != "Name" || cols[1].Name != "Age" {
				t.Errorf("columns = %+v", cols)
			}

			// Watches use the metadata format too.
			w, err := s.WatchWithContext(context.Background(), metav1.ListOptions{ResourceVersion: "7"})
			if err != nil {
				t.Fatal(err)
			}
			defer w.Stop()
			ev := <-w.ResultChan()
			if ev.Type != watch.Added || ev.Object.(*RowObject).Name != "n2" {
				t.Errorf("event = %+v", ev)
			}
		})
	}
}

func TestWatch(t *testing.T) {
	srv := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, watchEvent("ADDED", tableJSON("11", true, podRow("c", "uid-c", "11", "0/1"))))
		fmt.Fprint(w, watchEvent("BOOKMARK", `{"kind":"Table","apiVersion":"meta.k8s.io/v1","metadata":{"resourceVersion":"11"},"rows":[]}`))
		fmt.Fprint(w, watchEvent("MODIFIED", tableJSON("12", false, podRow("c", "uid-c", "12", "1/1"))))
		fmt.Fprint(w, watchEvent("DELETED", tableJSON("13", false, podRow("a", "uid-a", "13", "1/1"))))
		fmt.Fprint(w, watchEvent("ERROR", statusJSON(410, "Expired")))
	})
	s := newTestSource(t, srv, podsGVR, "default")
	w, err := s.WatchWithContext(context.Background(), metav1.ListOptions{ResourceVersion: "10", AllowWatchBookmarks: true, TimeoutSeconds: new(int64(300))})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	q := srv.lastRequest().URL.Query()
	if q.Get("watch") != "true" || q.Get("resourceVersion") != "10" || q.Get("allowWatchBookmarks") == "true" || q.Get("includeObject") != "Metadata" {
		t.Errorf("query = %v", q)
	}

	want := []struct {
		typ  watch.EventType
		name string
		rv   string
	}{{watch.Added, "c", "11"}, {watch.Modified, "c", "12"}, {watch.Deleted, "a", "13"}}
	for _, wnt := range want {
		ev := <-w.ResultChan()
		o, ok := ev.Object.(*RowObject)
		if ev.Type != wnt.typ || !ok || o.Name != wnt.name || o.ResourceVersion != wnt.rv {
			t.Fatalf("event = %s %+v, want %s %s@%s", ev.Type, ev.Object, wnt.typ, wnt.name, wnt.rv)
		}
	}
	ev := <-w.ResultChan()
	if ev.Type != watch.Error || !apierrors.IsResourceExpired(apierrors.FromObject(ev.Object)) {
		t.Errorf("event = %+v, want Expired error", ev)
	}
	if _, open := <-w.ResultChan(); open {
		t.Error("watch still open after end of stream")
	}
}

// TestInformer runs a real client-go informer over a Source.
func TestInformer(t *testing.T) {
	var watches sync.WaitGroup
	watches.Add(1)
	once := sync.Once{}
	srv := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") != "true" {
			fmt.Fprint(w, tableJSON("10", true, podRow("a", "uid-a", "5", "1/1")))
			return
		}
		fmt.Fprint(w, watchEvent("ADDED", tableJSON("11", true, podRow("b", "uid-b", "11", "1/1"))))
		w.(http.Flusher).Flush()
		once.Do(watches.Done)
		<-r.Context().Done()
	})
	s := newTestSource(t, srv, podsGVR, "default")
	inf := cache.NewSharedIndexInformerWithOptions(s, &RowObject{}, cache.SharedIndexInformerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go inf.RunWithContext(ctx)

	if !cache.WaitForCacheSync(ctx.Done(), inf.HasSynced) {
		t.Fatal("informer did not sync")
	}
	watches.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for len(inf.GetStore().ListKeys()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	keys := inf.GetStore().ListKeys()
	if len(keys) != 2 {
		t.Fatalf("store keys = %v, want default/a and default/b", keys)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for _, r := range srv.requests {
		if r.URL.Query().Get("sendInitialEvents") != "" {
			t.Errorf("informer used WatchList (%v); want LIST + WATCH", r.URL.Query())
		}
	}
}
