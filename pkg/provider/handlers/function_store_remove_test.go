package handlers

import (
	"fmt"
	"testing"
	"time"

	"github.gatech.edu/faasedge/fecore/pkg/provider/storage"
)

type memStorage struct {
	deleteFunctionErr error
	deletedContainers []string
}

func (m *memStorage) InsertFunction(storage.Function) error { return nil }
func (m *memStorage) GetAllFunctions() ([]storage.Function, error) {
	return nil, nil
}
func (m *memStorage) DeleteFunction(string) error             { return m.deleteFunctionErr }
func (m *memStorage) InsertContainer(storage.Container) error { return nil }
func (m *memStorage) GetContainersForFunction(string) ([]storage.Container, error) {
	return nil, nil
}
func (m *memStorage) GetAllContainers() ([]storage.Container, error) { return nil, nil }
func (m *memStorage) DeleteContainer(name string) error {
	m.deletedContainers = append(m.deletedContainers, name)
	return nil
}

func lockIsFree(t *testing.T, fs *FunctionStore) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fs.dfMu.Lock()
		fs.dfMu.Unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("function store lock is still held")
	}
}

func TestRemoveMissingFunctionReleasesLock(t *testing.T) {
	store := &memStorage{}
	fs, err := InitFunctionStore(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.RemoveDeployedFunction("publish_post"); err == nil {
		t.Fatal("missing function should be reported")
	}
	lockIsFree(t, fs)
}

func TestRemoveFunctionDeletesKnownContainers(t *testing.T) {
	store := &memStorage{}
	fs, err := InitFunctionStore(store)
	if err != nil {
		t.Fatal(err)
	}
	fs.deployedFunctions["timeline"] = &Function{
		name:           "timeline",
		activeReplicas: map[string]string{"timeline-a": "10.62.0.8"},
		idleReplicas:   map[string]string{"timeline-b": "10.62.0.9"},
	}
	if err := fs.RemoveDeployedFunction("timeline"); err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.deployedFunctions["timeline"]; ok {
		t.Fatal("timeline remained in the memory map")
	}
	if len(store.deletedContainers) != 2 {
		t.Fatalf("containers deleted: %v", store.deletedContainers)
	}
	lockIsFree(t, fs)
}

func TestRemoveStorageErrorReleasesLock(t *testing.T) {
	store := &memStorage{deleteFunctionErr: fmt.Errorf("db down")}
	fs, err := InitFunctionStore(store)
	if err != nil {
		t.Fatal(err)
	}
	fs.deployedFunctions["publish_post"] = &Function{name: "publish_post"}
	if err := fs.RemoveDeployedFunction("publish_post"); err == nil {
		t.Fatal("storage error should be returned")
	}
	if _, ok := fs.deployedFunctions["publish_post"]; !ok {
		t.Fatal("memory entry removed despite storage error")
	}
	lockIsFree(t, fs)
}
