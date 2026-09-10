package coordinator

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/JyotinderSingh/task-queue/pkg/grpcapi"
	"github.com/jackc/pgx/v4/pgxpool"

	"github.com/JyotinderSingh/task-queue/pkg/clock"
	"github.com/JyotinderSingh/task-queue/pkg/common"
	"github.com/JyotinderSingh/task-queue/pkg/materializer"
	"github.com/JyotinderSingh/task-queue/pkg/model"
	"github.com/JyotinderSingh/task-queue/pkg/reaper"
)

const (
	shutdownTimeout  = 5 * time.Second
	defaultMaxMisses = 1
	scanInterval     = 10 * time.Second
	// dispatchBatchSize bounds how many runs one scan offers to workers. The
	// scan holds row locks while it talks to workers over the network, so the
	// batch is kept small enough that the transaction stays short.
	dispatchBatchSize = 50
)

type CoordinatorServer struct {
	pb.UnimplementedCoordinatorServiceServer
	serverPort          string
	listener            net.Listener
	grpcServer          *grpc.Server
	WorkerPool          map[uint32]*workerInfo
	WorkerPoolMutex     sync.Mutex
	WorkerPoolKeys      []uint32
	WorkerPoolKeysMutex sync.RWMutex
	maxHeartbeatMisses  uint8
	heartbeatInterval   time.Duration
	roundRobinIndex     uint32
	dbConnectionString  string
	dbPool              *pgxpool.Pool
	clock               clock.Clock
	materializer        *materializer.Materializer
	reaper              *reaper.Reaper
	ctx                 context.Context    // The root context for all goroutines
	cancel              context.CancelFunc // Function to cancel the context
	wg                  sync.WaitGroup     // WaitGroup to wait for all goroutines to finish
}

type workerInfo struct {
	heartbeatMisses     uint8
	address             string
	grpcConnection      *grpc.ClientConn
	workerServiceClient pb.WorkerServiceClient
}

// NewServer initializes and returns a new Server instance.
func NewServer(port string, dbConnectionString string) *CoordinatorServer {
	return NewServerWithClock(port, dbConnectionString, clock.Real{})
}

// NewServerWithClock is used by tests, which drive time by hand.
func NewServerWithClock(port string, dbConnectionString string, clk clock.Clock) *CoordinatorServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &CoordinatorServer{
		WorkerPool:         make(map[uint32]*workerInfo),
		maxHeartbeatMisses: defaultMaxMisses,
		heartbeatInterval:  common.DefaultHeartbeat,
		dbConnectionString: dbConnectionString,
		serverPort:         port,
		clock:              clk,
		ctx:                ctx,
		cancel:             cancel,
	}
}

// Start initiates the server's operations.
func (s *CoordinatorServer) Start() error {
	var err error
	go s.manageWorkerPool()

	if err = s.startGRPCServer(); err != nil {
		return fmt.Errorf("gRPC server start failed: %w", err)
	}

	s.dbPool, err = common.ConnectToDatabase(s.ctx, s.dbConnectionString)
	if err != nil {
		return err
	}
	s.materializer = materializer.New(s.dbPool, s.clock)
	s.reaper = reaper.New(s.dbPool, s.clock)

	go s.scanDatabase()

	return s.awaitShutdown()
}

func (s *CoordinatorServer) startGRPCServer() error {
	var err error
	s.listener, err = net.Listen("tcp", s.serverPort)
	if err != nil {
		return err
	}

	log.Printf("Starting gRPC server on %s\n", s.serverPort)
	s.grpcServer = grpc.NewServer()
	pb.RegisterCoordinatorServiceServer(s.grpcServer, s)

	go func() {
		if err := s.grpcServer.Serve(s.listener); err != nil {
			log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	return nil
}

func (s *CoordinatorServer) awaitShutdown() error {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	return s.Stop()
}

// Stop gracefully shuts down the server.
func (s *CoordinatorServer) Stop() error {
	// Signal all goroutines to stop
	s.cancel()
	// Wait for all goroutines to finish
	s.wg.Wait()

	s.WorkerPoolMutex.Lock()
	defer s.WorkerPoolMutex.Unlock()
	for _, worker := range s.WorkerPool {
		if worker.grpcConnection != nil {
			worker.grpcConnection.Close()
		}
	}

	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
	}

	if s.listener != nil {
		return s.listener.Close()
	}

	s.dbPool.Close()
	return nil
}

// UpdateRunStatus records what a worker reports about a run. Statuses the
// platform owns - missed, cancelled - are never set from here.
func (s *CoordinatorServer) UpdateRunStatus(ctx context.Context, req *pb.UpdateRunStatusRequest) (*pb.UpdateRunStatusResponse, error) {
	now := s.clock.Now()

	switch req.GetStatus() {
	case pb.RunStatus_RUNNING:
		_, err := s.dbPool.Exec(ctx, `UPDATE runs SET status = $2, started_at = $3
			WHERE id = $1`, req.GetRunId(), model.RunRunning, now)
		if err != nil {
			return nil, err
		}

	case pb.RunStatus_SUCCEEDED, pb.RunStatus_FAILED, pb.RunStatus_BUDGET_EXCEEDED:
		status := model.RunSucceeded
		if req.GetStatus() == pb.RunStatus_FAILED {
			status = model.RunFailed
		}
		if req.GetStatus() == pb.RunStatus_BUDGET_EXCEEDED {
			status = model.RunBudgetExceeded
		}

		_, err := s.dbPool.Exec(ctx, `UPDATE runs
			SET status = $2, finished_at = $3, output = $4, error = $5,
				prompt_tokens = $6, completion_tokens = $7, lease_expires_at = NULL
			WHERE id = $1`,
			req.GetRunId(), status, now, req.GetOutput(), req.GetError(),
			req.GetPromptTokens(), req.GetCompletionTokens())
		if err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unsupported run status: %v", req.GetStatus())
	}

	return &pb.UpdateRunStatusResponse{Success: true}, nil
}

// RenewLease extends a worker's claim on a run it is still executing.
func (s *CoordinatorServer) RenewLease(ctx context.Context, req *pb.RenewLeaseRequest) (*pb.RenewLeaseResponse, error) {
	tag, err := s.dbPool.Exec(ctx, `UPDATE runs SET lease_expires_at = $2
		WHERE id = $1 AND status = $3`,
		req.GetRunId(), s.clock.Now().Add(reaper.LeaseTTL), model.RunRunning)
	if err != nil {
		return nil, err
	}
	return &pb.RenewLeaseResponse{Success: tag.RowsAffected() == 1}, nil
}

func (s *CoordinatorServer) getNextWorker() *workerInfo {
	s.WorkerPoolKeysMutex.RLock()
	defer s.WorkerPoolKeysMutex.RUnlock()

	workerCount := len(s.WorkerPoolKeys)
	if workerCount == 0 {
		return nil
	}

	worker := s.WorkerPool[s.WorkerPoolKeys[s.roundRobinIndex%uint32(workerCount)]]
	s.roundRobinIndex++
	return worker
}

func (s *CoordinatorServer) SendHeartbeat(ctx context.Context, in *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	s.WorkerPoolMutex.Lock()
	defer s.WorkerPoolMutex.Unlock()

	workerID := in.GetWorkerId()

	if worker, ok := s.WorkerPool[workerID]; ok {
		// log.Println("Reset hearbeat miss for worker:", workerID)
		worker.heartbeatMisses = 0
	} else {
		log.Println("Registering worker:", workerID)
		conn, err := grpc.Dial(in.GetAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, err
		}

		s.WorkerPool[workerID] = &workerInfo{
			address:             in.GetAddress(),
			grpcConnection:      conn,
			workerServiceClient: pb.NewWorkerServiceClient(conn),
		}

		s.WorkerPoolKeysMutex.Lock()
		defer s.WorkerPoolKeysMutex.Unlock()

		workerCount := len(s.WorkerPool)
		s.WorkerPoolKeys = make([]uint32, 0, workerCount)
		for k := range s.WorkerPool {
			s.WorkerPoolKeys = append(s.WorkerPoolKeys, k)
		}

		log.Println("Registered worker:", workerID)
	}

	return &pb.HeartbeatResponse{Acknowledged: true}, nil
}

func (s *CoordinatorServer) scanDatabase() {
	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Schedules become runs before runs are dispatched, so a schedule
			// due this tick is picked up on the same tick.
			go s.tick()
		case <-s.ctx.Done():
			log.Println("Shutting down database scanner.")
			return
		}
	}
}

// dispatchPendingRuns offers due runs to workers.
//
// A run stays pending until a worker accepts it. That is what makes dispatch
// safe when workers are saturated or absent: the run waits for the next scan
// instead of being marked as picked up and then dropped.
func (s *CoordinatorServer) dispatchPendingRuns() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	now := s.clock.Now()

	tx, err := s.dbPool.Begin(ctx)
	if err != nil {
		log.Printf("Unable to start transaction: %v\n", err)
		return
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `SELECT id, agent_id FROM runs
		WHERE status = $1 AND scheduled_for <= $2
		ORDER BY scheduled_for
		FOR UPDATE SKIP LOCKED
		LIMIT $3`, model.RunPending, now, dispatchBatchSize)
	if err != nil {
		log.Printf("Error querying pending runs: %v\n", err)
		return
	}

	var pending []*pb.RunRequest
	for rows.Next() {
		var runID, agentID string
		if err := rows.Scan(&runID, &agentID); err != nil {
			log.Printf("Failed to scan row: %v\n", err)
			continue
		}
		pending = append(pending, &pb.RunRequest{RunId: runID, AgentId: agentID})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("Error iterating rows: %v\n", err)
		return
	}

	for _, run := range pending {
		accepted, err := s.offerRunToWorker(ctx, run)
		if err != nil {
			log.Printf("Failed to offer run %s: %v\n", run.GetRunId(), err)
			break
		}
		if !accepted {
			// Every worker is saturated. Leaving the rest pending is the
			// backpressure: they are offered again on the next scan.
			break
		}

		if _, err := tx.Exec(ctx, `UPDATE runs
			SET status = $2, picked_at = $3, lease_expires_at = $4
			WHERE id = $1`,
			run.GetRunId(), model.RunRunning, now, now.Add(reaper.LeaseTTL)); err != nil {
			log.Printf("Failed to mark run %s as running: %v\n", run.GetRunId(), err)
			continue
		}
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("Failed to commit transaction: %v\n", err)
	}
}

// offerRunToWorker tries each worker once, so that one saturated worker does not
// hold up a run another worker could take.
func (s *CoordinatorServer) offerRunToWorker(ctx context.Context, run *pb.RunRequest) (bool, error) {
	s.WorkerPoolKeysMutex.RLock()
	workerCount := len(s.WorkerPoolKeys)
	s.WorkerPoolKeysMutex.RUnlock()

	if workerCount == 0 {
		return false, errors.New("no workers available")
	}

	for attempt := 0; attempt < workerCount; attempt++ {
		worker := s.getNextWorker()
		if worker == nil {
			return false, errors.New("no workers available")
		}

		response, err := worker.workerServiceClient.SubmitRun(ctx, run)
		if err != nil {
			log.Printf("Worker at %s refused run %s: %v", worker.address, run.GetRunId(), err)
			continue
		}
		if response.GetAccepted() {
			return true, nil
		}
	}

	return false, nil
}

func (s *CoordinatorServer) manageWorkerPool() {
	s.wg.Add(1)
	defer s.wg.Done()

	ticker := time.NewTicker(time.Duration(s.maxHeartbeatMisses) * s.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.removeInactiveWorkers()
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *CoordinatorServer) removeInactiveWorkers() {
	s.WorkerPoolMutex.Lock()
	defer s.WorkerPoolMutex.Unlock()

	for workerID, worker := range s.WorkerPool {
		if worker.heartbeatMisses > s.maxHeartbeatMisses {

			log.Printf("Removing inactive worker: %d\n", workerID)
			worker.grpcConnection.Close()
			delete(s.WorkerPool, workerID)

			s.WorkerPoolKeysMutex.Lock()

			workerCount := len(s.WorkerPool)
			s.WorkerPoolKeys = make([]uint32, 0, workerCount)
			for k := range s.WorkerPool {
				s.WorkerPoolKeys = append(s.WorkerPoolKeys, k)
			}

			s.WorkerPoolKeysMutex.Unlock()
		} else {
			worker.heartbeatMisses++
		}
	}
}

// tick runs the coordinator's periodic work: schedules become runs, due runs are
// offered to workers, and runs whose worker went away are failed.
func (s *CoordinatorServer) tick() {
	s.materializeSchedules()
	s.dispatchPendingRuns()
	s.reapExpiredRuns()
}

// materializeSchedules expands due schedules into runs.
func (s *CoordinatorServer) materializeSchedules() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	created, err := s.materializer.RunOnce(ctx)
	if err != nil {
		log.Printf("Failed to materialize schedules: %v", err)
		return
	}
	if created > 0 {
		log.Printf("Materialized %d run(s) from schedules", created)
	}
}

// reapExpiredRuns fails runs whose worker stopped renewing their lease.
func (s *CoordinatorServer) reapExpiredRuns() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reaped, err := s.reaper.RunOnce(ctx)
	if err != nil {
		log.Printf("Failed to reap expired runs: %v", err)
		return
	}
	if reaped > 0 {
		log.Printf("Failed %d run(s) whose worker stopped reporting", reaped)
	}
}
