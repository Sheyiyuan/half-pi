package remoteexec_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/hub"
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/remoteexec"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

func TestAuthorityClosePersistsLostAndWakesWaiter(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "authority-close.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	registry := remoteexec.NewRegistry(db)
	if err := registry.Create("shutdown-run", "session-1", "hand-1", "exec_command"); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition("shutdown-run", protocol.RunApproved); err != nil {
		t.Fatal(err)
	}
	if err := registry.Transition("shutdown-run", protocol.RunSent); err != nil {
		t.Fatal(err)
	}
	done, release, ok := registry.Wait("shutdown-run")
	if !ok {
		t.Fatal("run was not registered")
	}
	defer release()

	authority := remoteexec.NewAuthority(hub.New(), registry, nil, nil)
	if err := authority.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Registry.Wait was not woken by Authority.Close")
	}

	run, err := db.GetRemoteRun("shutdown-run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != protocol.RunLost || run.Error != "service_shutdown" {
		t.Fatalf("persisted run = %+v", run)
	}
	events, err := db.ListRemoteRunEvents("shutdown-run")
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Type != "service_shutdown" || last.ToStatus != protocol.RunLost {
		t.Fatalf("last audit event = %+v", last)
	}
}
