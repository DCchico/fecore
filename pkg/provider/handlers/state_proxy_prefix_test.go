package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestBFSPrefixDoesNotWriteSocialFile(t *testing.T) {
	vdag := "unit-bfs-prefix"
	os.Remove("/dev/shm/sn_" + vdag)
	os.Remove("/dev/shm/bfs_" + vdag)
	defer os.Remove("/dev/shm/sn_" + vdag)
	defer os.Remove("/dev/shm/bfs_" + vdag)
	handler := NewStateProxyHandler()

	body, _ := json.Marshal(map[string]interface{}{
		"vdag_id": vdag,
		"op":      "put",
		"prefix":  "bfs",
		"data":    []byte(`{"version":0}`),
	})
	put := httptest.NewRecorder()
	handler.HandleSocialState(put, httptest.NewRequest(http.MethodPost, "/social-state", bytes.NewReader(body)))
	if put.Code != http.StatusOK {
		t.Fatalf("put status %d", put.Code)
	}
	if _, err := os.Stat("/dev/shm/sn_" + vdag); !os.IsNotExist(err) {
		t.Fatalf("social file was written: %v", err)
	}
	if _, err := os.Stat("/dev/shm/bfs_" + vdag); err != nil {
		t.Fatal(err)
	}

	got := httptest.NewRecorder()
	handler.HandleSocialState(got, httptest.NewRequest(http.MethodGet, "/social-state?vdag_id="+vdag+"&prefix=bfs", nil))
	var fetched StateResponse
	if err := json.Unmarshal(got.Body.Bytes(), &fetched); err != nil || !fetched.Success || string(fetched.Data) != `{"version":0}` {
		t.Fatalf("get %+v %v", fetched, err)
	}

	rejectedBody, _ := json.Marshal(map[string]interface{}{"vdag_id": vdag, "op": "put", "prefix": "vt", "data": []byte("x")})
	rejected := httptest.NewRecorder()
	handler.HandleSocialState(rejected, httptest.NewRequest(http.MethodPost, "/social-state", bytes.NewReader(rejectedBody)))
	var refused StateResponse
	if err := json.Unmarshal(rejected.Body.Bytes(), &refused); err != nil || refused.Success {
		t.Fatalf("camera prefix was accepted: %+v %v", refused, err)
	}
}
