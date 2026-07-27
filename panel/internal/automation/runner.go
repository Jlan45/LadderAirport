// Package automation executes persistent DNS and certificate jobs.
package automation

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ladderairport/panel/internal/store"
)

const (
	defaultPollInterval = 2 * time.Second
	defaultLease        = 2 * time.Minute
)

type DNSService interface {
	Reconcile(ctx context.Context, domainID string) error
	Cleanup(ctx context.Context, domainID string) error
}

type CertificateService interface {
	Issue(ctx context.Context, certificateID string) error
}

type Runner struct {
	Store        *store.Store
	DNS          DNSService
	Certificates CertificateService
	Owner        string
	PollInterval time.Duration
	Lease        time.Duration
	BatchSize    int
	Now          func() time.Time
}

func (r *Runner) Run(ctx context.Context) {
	if r == nil || r.Store == nil {
		log.Printf("自动化任务执行器未启动：数据存储不可用")
		return
	}
	if r.Owner == "" {
		r.Owner = fmt.Sprintf("panel-%d", time.Now().UnixNano())
	}
	ticker := time.NewTicker(r.pollInterval())
	defer ticker.Stop()
	workers := make(chan struct{}, r.batchSize())
	var running sync.WaitGroup
	log.Printf("DNS/ACME 自动化任务执行器已启动（轮询=%s）", r.pollInterval())
	for {
		r.runAsync(ctx, workers, &running)
		select {
		case <-ctx.Done():
			running.Wait()
			return
		case <-ticker.C:
		}
	}
}

func (r *Runner) runAsync(ctx context.Context, workers chan struct{}, running *sync.WaitGroup) {
	now := r.now()
	r.enqueueDueDomains(now)
	r.enqueueDueCertificates(now)
	available := cap(workers) - len(workers)
	if available <= 0 {
		return
	}
	jobs, err := r.Store.ClaimAutomationJobs(r.Owner, now, r.lease(), available)
	if err != nil {
		log.Printf("领取自动化任务失败：%v", err)
		return
	}
	for i := range jobs {
		job := jobs[i]
		workers <- struct{}{}
		running.Add(1)
		go func() {
			defer func() {
				<-workers
				running.Done()
			}()
			r.execute(ctx, &job)
		}()
	}
}

func (r *Runner) runOnce(ctx context.Context) {
	now := r.now()
	r.enqueueDueDomains(now)
	r.enqueueDueCertificates(now)
	jobs, err := r.Store.ClaimAutomationJobs(r.Owner, now, r.lease(), r.batchSize())
	if err != nil {
		log.Printf("领取自动化任务失败：%v", err)
		return
	}
	for i := range jobs {
		if ctx.Err() != nil {
			return
		}
		r.execute(ctx, &jobs[i])
	}
}

func (r *Runner) enqueueDueCertificates(now time.Time) {
	certificates, err := r.Store.ListProtocolCertificatesDue(now.Unix(), r.batchSize()*4)
	if err != nil {
		log.Printf("查询待签发协议证书失败：%v", err)
		return
	}
	for i := range certificates {
		if _, err := r.Store.EnqueueAutomationJob(&store.AutomationJob{
			Type: "certificate.issue", TargetType: "protocol_certificate",
			TargetID: certificates[i].ID, NextRunUnix: now.Unix(),
		}); err != nil {
			log.Printf("为证书 %s 创建签发任务失败：%v", certificates[i].ID, err)
		}
	}
}

func (r *Runner) enqueueDueDomains(now time.Time) {
	domains, err := r.Store.ListManagedDomainsDue(now.Unix(), r.batchSize()*4)
	if err != nil {
		log.Printf("查询待同步域名失败：%v", err)
		return
	}
	for i := range domains {
		if _, err := r.Store.EnqueueAutomationJob(&store.AutomationJob{
			Type: "dns.reconcile", TargetType: "managed_domain",
			TargetID: domains[i].ID, NextRunUnix: now.Unix(),
		}); err != nil {
			log.Printf("为域名 %s 创建同步任务失败：%v", domains[i].FQDN, err)
		}
	}
}

func (r *Runner) execute(ctx context.Context, job *store.AutomationJob) {
	started := r.now()
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		interval := r.lease() / 3
		if interval < time.Second {
			interval = time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopHeartbeat:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := r.Store.RenewAutomationJobLease(
					job.ID, r.Owner, r.now(), r.lease(),
				); err != nil {
					log.Printf("续期自动化任务 %s 租约失败：%v", job.ID, err)
					return
				}
			}
		}
	}()
	err := r.dispatch(ctx, job)
	close(stopHeartbeat)
	<-heartbeatDone
	state := "success"
	nextRun := int64(0)
	message := ""
	if err != nil {
		state = "retry_wait"
		message = err.Error()
		nextRun = started.Add(jobRetryDelay(job.Attempt)).Unix()
		if job.Type != "dns.reconcile" && job.Type != "dns.cleanup" &&
			job.Type != "certificate.issue" {
			state = "failed"
			nextRun = 0
		}
	}
	if finishErr := r.Store.FinishAutomationJob(job.ID, r.Owner, state, message, nextRun); finishErr != nil {
		log.Printf("完成自动化任务 %s 失败：%v", job.ID, finishErr)
		return
	}
	outcome := "成功"
	if err != nil {
		outcome = "失败"
		log.Printf("自动化任务 %s（%s/%s）失败：%v", job.ID, job.Type, job.TargetID, err)
	}
	_ = r.Store.AddAutomationAudit(&store.AutomationAuditLog{
		Action: job.Type, TargetType: job.TargetType, TargetID: job.TargetID,
		Actor: r.Owner, Outcome: outcome, Detail: message,
	})
}

func (r *Runner) dispatch(ctx context.Context, job *store.AutomationJob) error {
	switch job.Type {
	case "dns.reconcile":
		if r.DNS == nil {
			return fmt.Errorf("DNS 自动化服务不可用")
		}
		return r.DNS.Reconcile(ctx, job.TargetID)
	case "dns.cleanup":
		if r.DNS == nil {
			return fmt.Errorf("DNS 自动化服务不可用")
		}
		return r.DNS.Cleanup(ctx, job.TargetID)
	case "certificate.issue":
		if r.Certificates == nil {
			return fmt.Errorf("证书自动化服务不可用")
		}
		return r.Certificates.Issue(ctx, job.TargetID)
	default:
		return fmt.Errorf("不支持的自动化任务类型：%s", job.Type)
	}
}

func (r *Runner) pollInterval() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return defaultPollInterval
}

func (r *Runner) lease() time.Duration {
	if r.Lease > 0 {
		return r.Lease
	}
	return defaultLease
}

func (r *Runner) batchSize() int {
	if r.BatchSize > 0 && r.BatchSize <= 100 {
		return r.BatchSize
	}
	return 10
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func jobRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return time.Minute
	case 2:
		return 5 * time.Minute
	case 3:
		return 15 * time.Minute
	case 4:
		return time.Hour
	default:
		return 6 * time.Hour
	}
}
