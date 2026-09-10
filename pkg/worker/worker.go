package worker

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

	"github.com/JyotinderSingh/task-queue/pkg/common"
	pb "github.com/JyotinderSingh/task-queue/pkg/grpcapi"
	"github.com/JyotinderSingh/task-queue/pkg/health"
	"github.com/JyotinderSingh/task-queue/pkg/reaper"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	// workerPoolSize is how many runs one worker executes concurrently.
	workerPoolSize = 5
	// runQueueSize bounds what a worker will accept. Past it the worker
	// declines, so the coordinator routes elsewhere instead of queueing work
	// behind runs that take minutes.
	runQueueSize = 5
)

// Executor performs the work of a run. It is an interface so that the agent
// loop can be developed and tested apart from the dispatch machinery.
type Executor interface {
	Execute(ctx context.Context, runID, agentID string) (Result, error)
}

// Result is what a completed run reports back.
type Result struct {
	Output           string
	PromptTokens     int
	CompletionTokens int
	// BudgetExceeded distinguishes a run stopped by its limits from one that
	// failed, so history can be read without opening every trace.
	BudgetExceeded bool
}

// noopExecutor is a placeholder that completes immediately. It exists so the
// dispatch path is exercisable before the agent loop lands, and is replaced
// wholesale rather than extended.
type noopExecutor struct{}

func (noopExecutor) Execute(ctx context.Context, runID, agentID string) (Result, error) {
	return Result{}, nil
}

// WorkerServer represents a gRPC server for handling worker tasks.
type WorkerServer struct {
	pb.UnimplementedWorkerServiceServer
	id                       uint32
	serverPort               string
	coordinatorAddress       string
	listener                 net.Listener
	grpcServer               *grpc.Server
	coordinatorConnection    *grpc.ClientConn
	coordinatorServiceClient pb.CoordinatorServiceClient
	heartbeatInterval        time.Duration
	runQueue                 chan *pb.RunRequest
	executor                 Executor
	// draining is set when the worker is shutting down. A draining worker
	// declines new runs while finishing the ones it already has, so a rolling
	// deploy does not kill work mid-flight.
	draining     bool
	drainingLock sync.RWMutex
	// healthAddress is where liveness and readiness are served. Empty disables
	// them, which is what tests want when they run several workers in one
	// process.
	healthAddress string
	healthServer  *health.Server
	ctx           context.Context    // The root context for all goroutines
	cancel        context.CancelFunc // Function to cancel the context
	wg            sync.WaitGroup     // WaitGroup to wait for all goroutines to finish
}

// NewServer creates and returns a new WorkerServer.
func NewServer(port string, coordinator string) *WorkerServer {
	return NewServerWithExecutor(port, coordinator, noopExecutor{})
}

// SetHealthAddress sets where probes are served. It must be called before Start.
func (w *WorkerServer) SetHealthAddress(address string) {
	w.healthAddress = address
}

// NewServerWithExecutor builds a worker around a specific executor.
func NewServerWithExecutor(port string, coordinator string, executor Executor) *WorkerServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkerServer{
		id:                 uuid.New().ID(),
		serverPort:         port,
		coordinatorAddress: coordinator,
		heartbeatInterval:  common.DefaultHeartbeat,
		runQueue:           make(chan *pb.RunRequest, runQueueSize),
		executor:           executor,
		ctx:                ctx,
		cancel:             cancel,
	}
}

// Start initializes and starts the WorkerServer.
func (w *WorkerServer) Start() error {
	w.startWorkerPool(workerPoolSize)

	if err := w.connectToCoordinator(); err != nil {
		return fmt.Errorf("failed to connect to coordinator: %w", err)
	}
	defer w.closeGRPCConnection()

	go w.periodicHeartbeat()

	if err := w.startGRPCServer(); err != nil {
		return fmt.Errorf("gRPC server start failed: %w", err)
	}

	if w.healthAddress != "" {
		var err error
		// A draining worker is alive and must not be killed - it is finishing
		// runs the coordinator believes are its own - but it must not be given
		// anything new either.
		w.healthServer, err = health.Serve(w.healthAddress,
			func(context.Context) error { return nil },
			func(context.Context) error {
				if w.isDraining() {
					return errors.New("draining")
				}
				return nil
			})
		if err != nil {
			return fmt.Errorf("health server start failed: %w", err)
		}
	}

	return w.awaitShutdown()
}

func (w *WorkerServer) connectToCoordinator() error {
	log.Println("Connecting to coordinator...")
	var err error
	w.coordinatorConnection, err = grpc.Dial(w.coordinatorAddress, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		return err
	}

	w.coordinatorServiceClient = pb.NewCoordinatorServiceClient(w.coordinatorConnection)
	log.Println("Connected to coordinator!")
	return nil
}

func (w *WorkerServer) periodicHeartbeat() {
	w.wg.Add(1)       // Add this goroutine to the waitgroup.
	defer w.wg.Done() // Signal this goroutine is done when the function returns

	ticker := time.NewTicker(w.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := w.sendHeartbeat(); err != nil {
				log.Printf("Failed to send heartbeat: %v", err)
				return
			}
		case <-w.ctx.Done():
			return
		}
	}
}

func (w *WorkerServer) sendHeartbeat() error {
	workerAddress := os.Getenv("WORKER_ADDRESS")
	if workerAddress == "" {
		// Fall back to using the listener address if WORKER_ADDRESS is not set
		workerAddress = w.listener.Addr().String()
	} else {
		workerAddress += w.serverPort
	}

	_, err := w.coordinatorServiceClient.SendHeartbeat(context.Background(), &pb.HeartbeatRequest{
		WorkerId: w.id,
		Address:  workerAddress,
	})
	return err
}

func (w *WorkerServer) startGRPCServer() error {
	var err error

	if w.serverPort == "" {
		// Find a free port using a temporary socket
		w.listener, err = net.Listen("tcp", ":0")                                // Bind to any available port
		w.serverPort = fmt.Sprintf(":%d", w.listener.Addr().(*net.TCPAddr).Port) // Get the assigned port
	} else {
		w.listener, err = net.Listen("tcp", w.serverPort)
	}

	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", w.serverPort, err)
	}

	log.Printf("Starting worker server on %s\n", w.serverPort)
	w.grpcServer = grpc.NewServer()
	pb.RegisterWorkerServiceServer(w.grpcServer, w)

	go func() {
		if err := w.grpcServer.Serve(w.listener); err != nil {
			log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	return nil
}

func (w *WorkerServer) awaitShutdown() error {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	return w.Stop()
}

// Stop gracefully shuts down the WorkerServer.
func (w *WorkerServer) Stop() error {
	// Decline new work first, so nothing is accepted that will not be run.
	w.drainingLock.Lock()
	w.draining = true
	w.drainingLock.Unlock()

	w.healthServer.Stop()

	// Signal all goroutines to stop
	w.cancel()
	// Wait for all goroutines to finish
	w.wg.Wait()

	w.closeGRPCConnection()
	log.Println("Worker server stopped")
	return nil
}

func (w *WorkerServer) closeGRPCConnection() {
	if w.grpcServer != nil {
		w.grpcServer.GracefulStop()
	}

	if w.listener != nil {
		if err := w.listener.Close(); err != nil {
			log.Printf("Error while closing the listener: %v", err)
		}
	}

	if err := w.coordinatorConnection.Close(); err != nil {
		log.Printf("Error while closing client connection with coordinator: %v", err)
	}
}

// SubmitRun accepts a run if there is room for it.
//
// Declining is the backpressure: with runs measured in minutes rather than
// seconds, accepting unboundedly would strand work behind a queue nobody is
// watching.
func (w *WorkerServer) SubmitRun(ctx context.Context, req *pb.RunRequest) (*pb.RunResponse, error) {
	if w.isDraining() {
		return &pb.RunResponse{
			RunId:    req.GetRunId(),
			Accepted: false,
			Message:  "worker is shutting down",
		}, nil
	}

	select {
	case w.runQueue <- req:
		return &pb.RunResponse{RunId: req.GetRunId(), Accepted: true, Message: "accepted"}, nil
	default:
		return &pb.RunResponse{
			RunId:    req.GetRunId(),
			Accepted: false,
			Message:  "worker is at capacity",
		}, nil
	}
}

func (w *WorkerServer) isDraining() bool {
	w.drainingLock.RLock()
	defer w.drainingLock.RUnlock()
	return w.draining
}

// startWorkerPool starts a pool of worker goroutines.
func (w *WorkerServer) startWorkerPool(numWorkers int) {
	for i := 0; i < numWorkers; i++ {
		w.wg.Add(1)
		go w.worker()
	}
}

// worker executes runs from the queue.
func (w *WorkerServer) worker() {
	defer w.wg.Done()

	for {
		select {
		case run := <-w.runQueue:
			w.executeRun(run)
		case <-w.ctx.Done():
			// Drain whatever was already accepted before stopping, so a
			// shutdown does not abandon runs the coordinator believes are ours.
			for {
				select {
				case run := <-w.runQueue:
					w.executeRun(run)
				default:
					return
				}
			}
		}
	}
}

// executeRun runs one agent and reports the outcome.
//
// The execution context is deliberately not derived from the worker's own
// context: a run already accepted is finished even while the worker is
// shutting down.
func (w *WorkerServer) executeRun(run *pb.RunRequest) {
	log.Printf("Starting run %s", run.GetRunId())

	w.reportStatus(&pb.UpdateRunStatusRequest{
		RunId:  run.GetRunId(),
		Status: pb.RunStatus_RUNNING,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stopRenewing := w.renewLeasePeriodically(ctx, run.GetRunId())
	defer stopRenewing()

	result, err := w.executor.Execute(ctx, run.GetRunId(), run.GetAgentId())
	if err != nil {
		log.Printf("Run %s failed: %v", run.GetRunId(), err)
		w.reportStatus(&pb.UpdateRunStatusRequest{
			RunId:  run.GetRunId(),
			Status: pb.RunStatus_FAILED,
			Error:  err.Error(),
		})
		return
	}

	status := pb.RunStatus_SUCCEEDED
	if result.BudgetExceeded {
		status = pb.RunStatus_BUDGET_EXCEEDED
	}

	w.reportStatus(&pb.UpdateRunStatusRequest{
		RunId:            run.GetRunId(),
		Status:           status,
		Output:           result.Output,
		PromptTokens:     int32(result.PromptTokens),
		CompletionTokens: int32(result.CompletionTokens),
	})
	log.Printf("Completed run %s", run.GetRunId())
}

// renewLeasePeriodically keeps the coordinator's claim on a run alive while it
// executes, and returns a function that stops the renewals.
func (w *WorkerServer) renewLeasePeriodically(ctx context.Context, runID string) func() {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		ticker := time.NewTicker(reaper.RenewalInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if _, err := w.coordinatorServiceClient.RenewLease(ctx,
					&pb.RenewLeaseRequest{RunId: runID}); err != nil {
					log.Printf("Failed to renew the lease on run %s: %v", runID, err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return cancel
}

// reportStatus tells the coordinator what happened to a run.
func (w *WorkerServer) reportStatus(request *pb.UpdateRunStatusRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := w.coordinatorServiceClient.UpdateRunStatus(ctx, request); err != nil {
		log.Printf("Failed to report status for run %s: %v", request.GetRunId(), err)
	}
}
