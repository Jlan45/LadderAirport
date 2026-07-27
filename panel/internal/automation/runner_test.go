package automation

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ladderairport/panel/internal/store"
)

type fakeDNSService struct {
	reconciled []string
	cleaned    []string
	err        error
}

func (f *fakeDNSService) Reconcile(_ context.Context, id string) error {
	f.reconciled = append(f.reconciled, id)
	return f.err
}

func (f *fakeDNSService) Cleanup(_ context.Context, id string) error {
	f.cleaned = append(f.cleaned, id)
	return f.err
}

func TestRunnerExecutesPersistentDNSJob(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	job, err := st.EnqueueAutomationJob(&store.AutomationJob{
		Type: "dns.reconcile", TargetType: "managed_domain", TargetID: "domain-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	dns := &fakeDNSService{}
	runner := &Runner{
		Store: st, DNS: dns, Owner: "test-worker",
		Now: func() time.Time { return time.Unix(2_000_000_000, 0) },
	}
	runner.runOnce(context.Background())
	got, err := st.GetAutomationJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "success" || len(dns.reconciled) != 1 || dns.reconciled[0] != "domain-1" {
		t.Fatalf("job=%+v reconciled=%v", got, dns.reconciled)
	}
}

func TestRunnerRetriesFailedCleanup(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	job, err := st.EnqueueAutomationJob(&store.AutomationJob{
		Type: "dns.cleanup", TargetType: "managed_domain", TargetID: "domain-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2_000_000_000, 0)
	dns := &fakeDNSService{err: errors.New("供应商暂时不可用")}
	runner := &Runner{Store: st, DNS: dns, Owner: "test-worker", Now: func() time.Time { return now }}
	runner.runOnce(context.Background())
	got, err := st.GetAutomationJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "retry_wait" || got.NextRunUnix != now.Add(time.Minute).Unix() ||
		len(dns.cleaned) != 1 {
		t.Fatalf("job=%+v cleaned=%v", got, dns.cleaned)
	}
}
