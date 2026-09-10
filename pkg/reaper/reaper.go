// Package reaper fails runs whose worker stopped reporting.
//
// On Kubernetes a worker dying mid-run is routine rather than exceptional:
// rolling deploys, node scale-down and evictions all do it. Without this, such a
// run sits in "running" forever and the dashboard lies about it - and because
// these agents run unattended and their whole output is a message, a run stuck
// silently is indistinguishable from one nobody read.
package reaper

import (
	"context"
	"time"

	"github.com/JyotinderSingh/task-queue/pkg/clock"
	"github.com/JyotinderSingh/task-queue/pkg/model"
	"github.com/jackc/pgx/v4/pgxpool"
)

// LeaseTTL is how long a claim on a run survives without renewal. It is
// comfortably longer than the renewal interval so that a slow network does not
// cost a worker its run.
const LeaseTTL = 60 * time.Second

// RenewalInterval is how often a worker executing a run extends its lease.
const RenewalInterval = 15 * time.Second

// lostWorkerMessage is recorded as the run's error so the failure reads as an
// infrastructure event rather than an agent problem.
const lostWorkerMessage = "worker stopped reporting; the run was abandoned"

// Reaper fails runs whose lease has expired.
type Reaper struct {
	pool  *pgxpool.Pool
	clock clock.Clock
}

func New(pool *pgxpool.Pool, clk clock.Clock) *Reaper {
	return &Reaper{pool: pool, clock: clk}
}

// RunOnce fails every run whose lease has expired, and reports how many.
func (r *Reaper) RunOnce(ctx context.Context) (int, error) {
	now := r.clock.Now()

	tag, err := r.pool.Exec(ctx, `UPDATE runs
		SET status = $1, error = $2, finished_at = $3
		WHERE status = $4 AND lease_expires_at IS NOT NULL AND lease_expires_at < $3`,
		model.RunFailed, lostWorkerMessage, now, model.RunRunning)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
