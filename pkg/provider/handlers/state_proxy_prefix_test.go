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

	overSocial, _ := json.Marshal(map[string]interface{}{
		"vdag_id": vdag,
		"op":      "put",
		"prefix":  "sn",
		"data":    bytes.Repeat([]byte("s"), (1<<20)+1),
	})
	over := httptest.NewRecorder()
	handler.HandleSocialState(over, httptest.NewRequest(http.MethodPost, "/social-state", bytes.NewReader(overSocial)))
	var tooBig StateResponse
	if err := json.Unmarshal(over.Body.Bytes(), &tooBig); err != nil || tooBig.Success {
		t.Fatalf("oversize social object was accepted: %+v %v", tooBig, err)
	}
	if _, err := os.Stat("/dev/shm/sn_" + vdag); !os.IsNotExist(err) {
		t.Fatalf("oversize social put created a file: %v", err)
	}

	rejectedBody, _ := json.Marshal(map[string]interface{}{"vdag_id": vdag, "op": "put", "prefix": "vt", "data": []byte("x")})
	rejected := httptest.NewRecorder()
	handler.HandleSocialState(rejected, httptest.NewRequest(http.MethodPost, "/social-state", bytes.NewReader(rejectedBody)))
	var refused StateResponse
	if err := json.Unmarshal(rejected.Body.Bytes(), &refused); err != nil || refused.Success {
		t.Fatalf("camera prefix was accepted: %+v %v", refused, err)
	}
}

func TestBFSMultiMegabyteReplaceIsExact(t *testing.T) {
	vdag := "unit-bfs-multimeg"
	path := "/dev/shm/bfs_" + vdag
	os.Remove(path)
	defer os.Remove(path)
	handler := NewStateProxyHandler()
	first := make([]byte, 4<<20)
	for i := range first {
		first[i] = byte(i)
	}
	binaryPut := func(version byte, payload []byte) StateResponse {
		body, _ := json.Marshal(map[string]interface{}{
			"vdag_id": vdag,
			"op":      "put",
			"prefix":  "bfs",
			"data":    append([]byte{version}, payload...),
		})
		rec := httptest.NewRecorder()
		handler.HandleSocialState(rec, httptest.NewRequest(http.MethodPost, "/social-state", bytes.NewReader(body)))
		var response StateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	if stored := binaryPut(1, first); !stored.Success {
		t.Fatalf("first put %+v", stored)
	}
	second := make([]byte, 4<<20)
	for i := range second {
		second[i] = byte(255 - (i % 251))
	}
	if stored := binaryPut(2, second); !stored.Success {
		t.Fatalf("replacement %+v", stored)
	}
	got := httptest.NewRecorder()
	handler.HandleSocialState(got, httptest.NewRequest(http.MethodGet, "/social-state?vdag_id="+vdag+"&prefix=bfs", nil))
	var fetched StateResponse
	if err := json.Unmarshal(got.Body.Bytes(), &fetched); err != nil || !fetched.Success {
		t.Fatalf("get %+v %v", fetched, err)
	}
	want := append([]byte{2}, second...)
	if !bytes.Equal(fetched.Data, want) {
		t.Fatalf("replacement length %d version %d", len(fetched.Data), fetched.Data[0])
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(8+len(want)) {
		t.Fatalf("file size %d", info.Size())
	}
	over := binaryPut(3, bytes.Repeat([]byte("x"), objectLimit("bfs")+1))
	if over.Success || over.Error == "" {
		t.Fatalf("oversize bfs object was stored: %+v", over)
	}
	got = httptest.NewRecorder()
	handler.HandleSocialState(got, httptest.NewRequest(http.MethodGet, "/social-state?vdag_id="+vdag+"&prefix=bfs", nil))
	if err := json.Unmarshal(got.Body.Bytes(), &fetched); err != nil || !bytes.Equal(fetched.Data, want) {
		t.Fatal("oversize put replaced the committed object")
	}
}
