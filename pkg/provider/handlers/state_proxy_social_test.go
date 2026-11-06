package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gorilla/mux"
)

func TestSocialStateRoundTripAndMissing(t *testing.T) {
	vdag := "unit-social-state"
	os.Remove("/dev/shm/sn_" + vdag)
	defer os.Remove("/dev/shm/sn_" + vdag)
	handler := NewStateProxyHandler()

	missing := httptest.NewRequest(http.MethodGet, "/social-state?vdag_id="+vdag, nil)
	rec := httptest.NewRecorder()
	handler.HandleSocialState(rec, missing)
	var miss StateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &miss); err != nil {
		t.Fatal(err)
	}
	if miss.Success || miss.Error != "missing" {
		t.Fatalf("missing response %+v", miss)
	}

	body, _ := json.Marshal(map[string]interface{}{"vdag_id": vdag, "op": "put", "data": []byte("[]")})
	put := httptest.NewRequest(http.MethodPost, "/social-state", bytes.NewReader(body))
	putRec := httptest.NewRecorder()
	handler.HandleSocialState(putRec, put)
	var stored StateResponse
	if err := json.Unmarshal(putRec.Body.Bytes(), &stored); err != nil || !stored.Success {
		t.Fatalf("put %+v %v", stored, err)
	}
	info, err := os.Stat("/dev/shm/sn_" + vdag)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 1<<20 {
		t.Fatalf("segment %d exceeds 1MiB", info.Size())
	}

	got := httptest.NewRecorder()
	handler.HandleSocialState(got, httptest.NewRequest(http.MethodGet, "/social-state?vdag_id="+vdag, nil))
	var fetched StateResponse
	if err := json.Unmarshal(got.Body.Bytes(), &fetched); err != nil || !fetched.Success || string(fetched.Data) != "[]" {
		t.Fatalf("get %+v %v", fetched, err)
	}
}

func TestCameraStateRouteRemainsRegistered(t *testing.T) {
	router := mux.NewRouter()
	NewStateProxyHandler().RegisterRoutes(router)
	for _, path := range []string{"/state", "/social-state"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		match := &mux.RouteMatch{}
		if !router.Match(req, match) {
			t.Fatalf("route %s is not registered", path)
		}
	}
}

func TestSocialPublicationReplacesInsteadOfMutatingReaderCopy(t *testing.T) {
	path := "/dev/shm/sn_unit-social-atomic"
	defer os.Remove(path)
	if err := publishSocialCopy(path, []byte("before")); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := publishSocialCopy(path, []byte("after")); err != nil {
		t.Fatal(err)
	}
	old := make([]byte, 6)
	if _, err := reader.Read(old); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != "before" || string(current) != "after" {
		t.Fatalf("old=%q current=%q", old, current)
	}
}
