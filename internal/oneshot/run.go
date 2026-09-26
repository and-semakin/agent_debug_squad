package oneshot

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/api"
	"github.com/and-semakin/agent_debug_squad/internal/config"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/judge"
	"github.com/and-semakin/agent_debug_squad/internal/orchestrator"
	"github.com/and-semakin/agent_debug_squad/internal/store"
	"github.com/and-semakin/agent_debug_squad/internal/workflow"
)

// Process exit codes for the run command.
const (
	exitSuccess        = 0
	exitFailure        = 1
	exitStartupFailure = 2
	exitCancelled      = 3
	exitSigint         = 130
	exitSigterm        = 143
)

// shutdownBudget is the shared graceful teardown budget: it starts once at
// terminal detection, signal, or fatal runtime error and is never restarted
// for successive stages.
var shutdownBudget = 30 * time.Second

type runner struct {
	stdout io.Writer
	stderr io.Writer

	cfg       domain.SessionConfig
	st        *store.Store
	requestID string

	// signal routing: the collector forwards the first signal to the active
	// phase handler.
	ctl struct {
		mu          sync.Mutex
		firstSignal string
		handler     func(sig string)
	}

	// startup phase state
	startupCancel context.CancelFunc

	// live phase state
	liveMu       sync.Mutex
	runtimeReady bool
	manager      *workflow.Manager
	executionID  string

	decisionMu   sync.Mutex
	decisionMade bool

	sigMu          sync.Mutex
	signalAccepted bool
	signalExitCode int

	fatalMu   sync.Mutex
	fatalErrs []error

	obsMu     sync.Mutex
	obsCancel context.CancelFunc

	teardownMu      sync.Mutex
	teardownStarted bool
}

// Main parses run arguments and executes the one-shot lifecycle. The returned
// error is non-nil only for argument errors, which the caller reports with
// CLI usage; every other outcome is diagnosed on stderr and reflected in the
// exit code. Exactly one JSON summary is written to stdout on orderly
// termination.
func Main(args []string, stdout, stderr io.Writer) (int, error) {
	r := &runner{stdout: stdout, stderr: stderr}
	return r.main(args)
}

func (r *runner) main(args []string) (int, error) {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(r.stderr)
	configPath := flags.String("config", "", "path to squad config file")
	requestID := flags.String("request-id", "", "durable workflow request identity")
	if err := flags.Parse(args); err != nil {
		return exitStartupFailure, err
	}
	if *configPath == "" {
		return exitStartupFailure, fmt.Errorf("--config is required")
	}
	if strings.TrimSpace(*requestID) == "" {
		return exitStartupFailure, fmt.Errorf("--request-id is required and must not be blank")
	}
	if flags.NArg() > 0 {
		return exitStartupFailure, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	r.requestID = *requestID

	stopSignals := r.startSignalCollector()
	defer stopSignals()

	return r.execute(*configPath), nil
}

// startSignalCollector keeps receiving SIGINT/SIGTERM for the whole lifetime
// and forwards the first signal to the current phase handler; later signals
// are ignored for lifecycle control.
func (r *runner) startSignalCollector() (stop func()) {
	sigC := make(chan os.Signal, 4)
	signal.Notify(sigC, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigC:
				name := signalName(sig)
				r.ctl.mu.Lock()
				if r.ctl.firstSignal == "" {
					r.ctl.firstSignal = name
				}
				handler := r.ctl.handler
				first := r.ctl.firstSignal
				r.ctl.mu.Unlock()
				if handler != nil {
					handler(first)
				}
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(sigC)
		close(done)
	}
}

func (r *runner) setHandler(handler func(sig string)) {
	r.ctl.mu.Lock()
	r.ctl.handler = handler
	r.ctl.mu.Unlock()
}

func signalName(sig os.Signal) string {
	switch sig {
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	default:
		return sig.String()
	}
}

func exitForSignal(name string) int {
	if name == "SIGINT" {
		return exitSigint
	}
	return exitSigterm
}

func exitForOutcome(state domain.WorkflowState) int {
	switch state {
	case domain.WorkflowSucceeded, domain.WorkflowCompletedWithError:
		return exitSuccess
	case domain.WorkflowFailed:
		return exitFailure
	case domain.WorkflowCancelled:
		return exitCancelled
	default:
		// A nonterminal durable state at exit means the process could not
		// finish its work; that is a failure, never a success.
		return exitFailure
	}
}

func (r *runner) logf(format string, args ...any) {
	if r.cfg.LogLevel != domain.LogLevelQuiet {
		fmt.Fprintf(r.stderr, format+"\n", args...)
	}
}

func (r *runner) logAlways(format string, args ...any) {
	fmt.Fprintf(r.stderr, format+"\n", args...)
}

func (r *runner) recordFatal(err error) {
	r.fatalMu.Lock()
	r.fatalErrs = append(r.fatalErrs, err)
	r.fatalMu.Unlock()
	r.logAlways("one-shot fatal error: %v", err)
	r.wakeObserver()
}

func (r *runner) wakeObserver() {
	r.obsMu.Lock()
	cancel := r.obsCancel
	r.obsMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *runner) hasFatal() bool {
	r.fatalMu.Lock()
	defer r.fatalMu.Unlock()
	return len(r.fatalErrs) > 0
}

func (r *runner) acceptedSignal() (bool, int) {
	r.sigMu.Lock()
	defer r.sigMu.Unlock()
	return r.signalAccepted, r.signalExitCode
}

func (r *runner) firstSignalName() string {
	r.ctl.mu.Lock()
	defer r.ctl.mu.Unlock()
	return r.ctl.firstSignal
}

// handleSignal routes the first signal to the active phase. During startup it
// aborts before any work started; during the live phase it makes the
// serialized cancellation decision; during teardown it is ignored.
func (r *runner) handleSignal(sig string) {
	r.decisionMu.Lock()
	if r.decisionMade {
		r.decisionMu.Unlock()
		return
	}
	r.decisionMu.Unlock()

	r.liveMu.Lock()
	ready := r.runtimeReady
	r.liveMu.Unlock()

	if !ready {
		r.decisionMu.Lock()
		r.decisionMade = true
		r.decisionMu.Unlock()
		if r.startupCancel != nil {
			r.startupCancel()
		}
		return
	}
	r.liveSignalDecision(sig)
}

// liveSignalDecision serializes the first signal's cancellation decision
// against terminal commitment. A decision accepted while the execution is
// still nonterminal claims the signal exit code; a terminal outcome that
// committed first keeps its normal mapping.
func (r *runner) liveSignalDecision(sig string) {
	r.decisionMu.Lock()
	if r.decisionMade {
		r.decisionMu.Unlock()
		return
	}
	r.decisionMade = true
	r.liveMu.Lock()
	manager, executionID, ready := r.manager, r.executionID, r.runtimeReady
	r.liveMu.Unlock()
	if !ready || manager == nil || executionID == "" {
		r.decisionMu.Unlock()
		return
	}
	r.decisionMu.Unlock()

	view, err := manager.Cancel(executionID, workflow.CancelOptions{})
	switch {
	case err != nil && errors.Is(err, workflow.ErrInvalidTransition):
		// Terminal commitment won the race; the normal exit mapping is
		// preserved.
	case err != nil:
		r.recordFatal(fmt.Errorf("record cancellation after %s: %w", sig, err))
	case view.State.Terminal():
		// A cancelled terminal outcome committed before this decision.
	default:
		r.sigMu.Lock()
		r.signalAccepted = true
		r.signalExitCode = exitForSignal(sig)
		r.sigMu.Unlock()
	}
}

// execute runs the whole one-shot lifecycle and returns the process exit
// code. Every exit path emits exactly one stdout summary.
func (r *runner) execute(configPath string) int {
	startupCtx, startupCancel := context.WithCancel(context.Background())
	defer startupCancel()
	r.startupCancel = startupCancel
	r.setHandler(r.handleSignal)

	cfg, err := r.loadConfig(configPath)
	if err != nil {
		return r.startupFailure(err, CleanupNotStarted)
	}
	if startupCtx.Err() != nil {
		return r.startupSignalExit()
	}

	if cfg.Workflow == nil {
		return r.startupFailure(fmt.Errorf("no workflow is configured"), CleanupNotStarted)
	}
	if err := config.ValidateWorkflowDefinition(*cfg.Workflow, cfg.Agents); err != nil {
		return r.startupFailure(fmt.Errorf("invalid workflow definition: %w", err), CleanupNotStarted)
	}
	fingerprint := workflow.DefinitionFingerprint(cfg, *cfg.Workflow)

	r.st = store.New(cfg)
	ownership, err := store.AcquireSessionOwnership(r.st.SessionDir())
	if err != nil {
		return r.startupFailure(err, CleanupNotStarted)
	}
	ownershipReleased := false
	var ownershipMu sync.Mutex
	releaseOwnership := func() error {
		ownershipMu.Lock()
		defer ownershipMu.Unlock()
		if ownershipReleased {
			return nil
		}
		ownershipReleased = true
		return ownership.Release()
	}
	// Backup release; the normal paths release explicitly as the last
	// resource after summary persistence.
	defer func() {
		if err := releaseOwnership(); err != nil {
			r.logAlways("one-shot: release session ownership: %v", err)
		}
	}()

	if startupCtx.Err() != nil {
		return r.startupSignalExit()
	}

	sel, err := preflight(r.st, cfg, r.requestID, fingerprint)
	if err != nil {
		return r.startupFailure(err, CleanupNotStarted)
	}
	if startupCtx.Err() != nil {
		return r.startupSignalExit()
	}

	if sel.terminal {
		return r.replayPath(sel, releaseOwnership)
	}
	return r.livePath(startupCtx, cfg, sel, releaseOwnership)
}

func (r *runner) loadConfig(configPath string) (domain.SessionConfig, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return domain.SessionConfig{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return domain.SessionConfig{}, fmt.Errorf("resolve home directory: %w", err)
	}
	machineBackends, err := config.LoadMachineBackends(home)
	if err != nil {
		return domain.SessionConfig{}, err
	}
	cfg.MachineBackends = machineBackends
	r.cfg = cfg
	return cfg, nil
}

// startupFailure exits 2 before dispatch: invalid arguments or configuration,
// missing workflow, request or ownership conflicts, or other pre-execution
// startup failures. No workflow summary file is created.
func (r *runner) startupFailure(err error, cleanupStatus string) int {
	r.logAlways("one-shot startup failure: %v", err)
	r.emitStartupSummary(exitStartupFailure, ReasonStartupFailure, cleanupStatus)
	return exitStartupFailure
}

// startupSignalExit covers a signal that arrived before an execution was
// selected: nothing was dispatched, so shutdown is immediate.
func (r *runner) startupSignalExit() int {
	name := r.firstSignalName()
	code := exitForSignal(name)
	r.logAlways("one-shot: %s received during startup; exiting before any work started", name)
	r.emitStartupSummary(code, ReasonSignal, CleanupNotStarted)
	return code
}

func (r *runner) emitStartupSummary(exitCode int, exitReason, cleanupStatus string) {
	summary := buildSummary(
		&domain.WorkflowSnapshot{RequestID: r.requestID},
		domain.WorkflowTaskCounts{}, exitCode, exitReason, r.firstSignalName(),
		CleanupReport{Status: cleanupStatus}, "", "", "",
	)
	summary.DecisionPaths = []string{}
	summary.ResponsePaths = []string{}
	r.writeSummaryJSON(summary)
}

// replayPath reports a selected terminal execution without backend
// initialization, judge calls, a listener, or new attempts.
func (r *runner) replayPath(sel selection, releaseOwnership func() error) int {
	snapshot := sel.snapshot
	var verifyErrs []error
	for _, taskID := range sortedTaskIDs(snapshot.Tasks) {
		task := snapshot.Tasks[taskID]
		for i := range task.Attempts {
			attempt := &task.Attempts[i]
			if attempt.State != domain.WorkflowAttemptSucceeded || attempt.ResultPath == "" {
				continue
			}
			if err := r.st.VerifyWorkflowArtifact(snapshot.ExecutionID, attempt.ResultPath, attempt.ResultSize, attempt.ResultSHA256); err != nil {
				verifyErrs = append(verifyErrs, err)
			}
		}
	}

	cleanup := CleanupReport{Status: CleanupComplete}
	exitCode := exitForOutcome(snapshot.State)
	exitReason := ReasonTerminalOutcome
	for _, err := range verifyErrs {
		cleanup.Status = CleanupIncomplete
		cleanup.Errors = append(cleanup.Errors, err.Error())
		r.logAlways("one-shot artifact verification failed: %v", err)
	}
	if len(verifyErrs) > 0 {
		exitCode = exitFailure
		exitReason = ReasonFatalError
	}

	workflowDir, _ := r.st.WorkflowDir(snapshot.ExecutionID)
	summary := buildSummary(&snapshot, countTasks(&snapshot), exitCode, exitReason, r.firstSignalName(), cleanup, r.st.SessionDir(), workflowDir, summarySnapshotPath(workflowDir))
	summary.DecisionPaths = collectDecisionPaths(r.st, &snapshot)

	// A replay replaces only the derived report; persistence failure fails
	// the command without rewriting the terminal execution.
	if err := r.st.WriteRunSummary(snapshot.ExecutionID, mustMarshal(summary)); err != nil {
		summary.SummaryPersisted = false
		summary.ExitCode = exitFailure
		summary.Cleanup.Errors = append(summary.Cleanup.Errors, err.Error())
		summary.Cleanup.Status = CleanupIncomplete
		if exitCode == exitSuccess || exitCode == exitCancelled {
			exitCode = exitFailure
		}
		r.logAlways("one-shot: persist run summary: %v", err)
	} else {
		summary.SummaryPersisted = true
	}

	if err := releaseOwnership(); err != nil {
		exitCode = exitFailure
		r.logAlways("one-shot: release session ownership: %v", err)
	}
	if !r.writeSummaryJSON(summary) {
		// The saved report remains the evidence of the workflow outcome; the
		// shell status and stderr take precedence over its recorded code.
		exitCode = exitFailure
	}
	return exitCode
}

// livePath owns a new or nonterminal selected execution until it settles.
func (r *runner) livePath(startupCtx context.Context, cfg domain.SessionConfig, sel selection, releaseOwnership func() error) int {
	// Bind the configured loopback address before backend initialization or
	// dispatch; a port conflict is a startup error, never permission to stop
	// the owner.
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return r.startupFailure(fmt.Errorf("bind control listener on %s: %w", addr, err), CleanupNotStarted)
	}

	// The orchestrator's lifecycle context is independent of signal handling:
	// owned work is cancelled explicitly during teardown, not by signals.
	orchCtx, orchCancel := context.WithCancel(context.Background())
	defer orchCancel()
	orch, err := orchestrator.NewWorkflowOnly(orchCtx, cfg, r.st)
	if err != nil {
		_ = listener.Close()
		return r.startupFailure(fmt.Errorf("initialize orchestrator: %w", err), CleanupNotStarted)
	}
	defer orch.Close()

	var judgeClient judge.Judge
	if declaresVerdicts(cfg.Workflow) {
		home, err := os.UserHomeDir()
		if err != nil {
			_ = listener.Close()
			orchCancel()
			return r.startupFailure(fmt.Errorf("resolve home directory: %w", err), CleanupNotStarted)
		}
		judgeClient, err = judge.Setup(cfg.Judge, cfg.Workflow, home, cfg.MachineBackends.JudgeProxyURL())
		if err != nil {
			_ = listener.Close()
			orchCancel()
			return r.startupFailure(fmt.Errorf("initialize verdict judge: %w", err), CleanupNotStarted)
		}
	}

	manager := workflow.NewManager(cfg, r.st, orch)
	if judgeClient != nil {
		manager.SetJudge(judgeClient)
	}

	executionID := sel.executionID
	if executionID != "" {
		manager.SetOneShotTarget(executionID)
		if err := manager.StartScoped(context.Background(), executionID); err != nil {
			_ = listener.Close()
			orchCancel()
			return r.startupFailure(fmt.Errorf("recover selected execution: %w", err), CleanupNotStarted)
		}
	} else {
		// Start the scheduler with an empty recovery scope, then create the
		// selected execution; the fence target installs inside the same
		// serialized step as creation.
		if err := manager.StartScoped(context.Background(), ""); err != nil {
			_ = listener.Close()
			orchCancel()
			return r.startupFailure(fmt.Errorf("start workflow scheduler: %w", err), CleanupNotStarted)
		}
		view, _, err := manager.CreateSelected(r.requestID)
		if err != nil {
			_ = listener.Close()
			orchCancel()
			return r.startupFailure(fmt.Errorf("create execution: %w", err), CleanupNotStarted)
		}
		executionID = view.ExecutionID
	}

	// The runtime is live from here on; failures exit 1 after bounded
	// cleanup instead of the startup code.
	r.liveMu.Lock()
	r.manager = manager
	r.executionID = executionID
	r.runtimeReady = true
	r.liveMu.Unlock()

	handler := api.New(orch, manager, cfg)
	handler.SetOneShotAdmission(api.OneShotAdmission{ExecutionID: executionID, OwnsRun: manager.OwnsRun})
	srv := &http.Server{Handler: handler}
	serveErrC := make(chan error, 1)
	go func() {
		serveErrC <- srv.Serve(listener)
	}()

	// A signal that raced with runtime startup still gets its serialized
	// cancellation decision instead of being lost.
	if startupCtx.Err() != nil {
		r.liveSignalDecision(r.firstSignalName())
	}

	controlURL := fmt.Sprintf("http://%s", listener.Addr())
	r.logAlways("one-shot run: execution %s (request %q)", executionID, r.requestID)
	r.logAlways("control URL: %s", controlURL)

	obsCtx, obsCancel := context.WithCancel(context.Background())
	defer obsCancel()
	r.obsMu.Lock()
	r.obsCancel = obsCancel
	r.obsMu.Unlock()

	go func() {
		if err := <-serveErrC; err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.recordFatal(fmt.Errorf("control listener failed: %w", err))
		}
	}()

	view, observeErr := manager.ObserveTerminal(obsCtx, executionID, r.interventionNotice(executionID, controlURL))

	fatal := r.hasFatal() || observeErr != nil
	if observeErr != nil && !errors.Is(observeErr, context.Canceled) {
		r.logAlways("one-shot: workflow manager failure: %v", observeErr)
	}
	if fatal && !view.State.Terminal() {
		// Prevent dispatch and attempt the same durable cancellation; the
		// primary error is preserved when storage does not permit it.
		if _, err := manager.Cancel(executionID, workflow.CancelOptions{}); err != nil {
			r.logAlways("one-shot: request cancellation during error cleanup: %v", err)
		}
	}

	cleanupReport := r.teardown(srv, manager, orch, orchCancel)

	finalSnapshot, err := r.st.LoadWorkflowSnapshot(executionID)
	if err != nil {
		r.recordFatal(fmt.Errorf("load final snapshot: %w", err))
		finalSnapshot = domain.WorkflowSnapshot{ExecutionID: executionID, RequestID: r.requestID, State: view.State, Revision: view.Revision}
	}

	exitCode := r.resolveExitCode(finalSnapshot.State, cleanupReport)
	exitReason := ReasonTerminalOutcome
	if accepted, _ := r.acceptedSignal(); accepted && !r.hasFatal() && cleanupReport.Status == CleanupComplete {
		exitReason = ReasonSignal
	} else if !finalSnapshot.State.Terminal() || r.hasFatal() || cleanupReport.Status != CleanupComplete {
		exitReason = ReasonFatalError
	}

	workflowDir, _ := r.st.WorkflowDir(executionID)
	summary := buildSummary(&finalSnapshot, countTasks(&finalSnapshot), exitCode, exitReason, r.firstSignalName(), cleanupReport, r.st.SessionDir(), workflowDir, summarySnapshotPath(workflowDir))
	summary.DecisionPaths = collectDecisionPaths(r.st, &finalSnapshot)

	// Persist the derived summary before stdout emission and ownership
	// release; it is best-effort evidence, never an input to recovery.
	if err := r.st.WriteRunSummary(executionID, mustMarshal(summary)); err != nil {
		summary.SummaryPersisted = false
		summary.ExitCode = exitFailure
		summary.Cleanup.Errors = append(summary.Cleanup.Errors, err.Error())
		summary.Cleanup.Status = CleanupIncomplete
		exitCode = exitFailure
		r.logAlways("one-shot: persist run summary: %v", err)
	} else {
		summary.SummaryPersisted = true
	}

	if err := releaseOwnership(); err != nil {
		exitCode = exitFailure
		r.logAlways("one-shot: release session ownership: %v", err)
	}

	if !r.writeSummaryJSON(summary) {
		// The saved report remains the evidence of the workflow outcome; the
		// shell status and stderr take precedence over its recorded code.
		exitCode = exitFailure
	}
	return exitCode
}

func (r *runner) resolveExitCode(state domain.WorkflowState, cleanup CleanupReport) int {
	code := exitForOutcome(state)
	if accepted, signalCode := r.acceptedSignal(); accepted {
		code = signalCode
	}
	if r.hasFatal() || cleanup.Status != CleanupComplete {
		code = exitFailure
	}
	return code
}

// interventionNotice reports nonterminal intervention states when they
// change, never on every poll, in quiet logging mode included.
func (r *runner) interventionNotice(executionID, controlURL string) func(domain.WorkflowExecutionView) {
	lastKey := ""
	return func(view domain.WorkflowExecutionView) {
		if view.State.Terminal() {
			return
		}
		intervening := view.State == domain.WorkflowNeedsAttention ||
			view.State == domain.WorkflowPaused ||
			view.State == domain.WorkflowCancelling ||
			len(view.PendingPermissions) > 0
		if !intervening {
			return
		}
		reasons := append([]string(nil), view.AttentionReasons...)
		sortStrings(reasons)
		key := fmt.Sprintf("%s|%v|%d", view.State, reasons, len(view.PendingPermissions))
		if key == lastKey {
			return
		}
		lastKey = key
		message := fmt.Sprintf("one-shot: execution %s requires intervention: state=%s", executionID, view.State)
		if len(reasons) > 0 {
			message += fmt.Sprintf(" reasons=%s", strings.Join(reasons, ", "))
		}
		if len(view.PendingPermissions) > 0 {
			message += fmt.Sprintf(" pending_permissions=%d", len(view.PendingPermissions))
		}
		message += fmt.Sprintf("; control URL: %s", controlURL)
		r.logAlways("%s", message)
	}
}

// teardown performs graceful cleanup under one shared budget: drain the
// control API, stop scheduling and judge activity, join owned workers, and
// cancel remaining owned contexts. Ownership release happens later, after
// summary persistence, as the last resource.
func (r *runner) teardown(srv *http.Server, manager *workflow.Manager, orch *orchestrator.Orchestrator, orchCancel context.CancelFunc) CleanupReport {
	r.teardownMu.Lock()
	if r.teardownStarted {
		r.teardownMu.Unlock()
		return CleanupReport{Status: CleanupComplete}
	}
	r.teardownStarted = true
	r.teardownMu.Unlock()

	r.setHandler(nil) // teardown signals are ignored for lifecycle control.

	report := CleanupReport{Status: CleanupComplete, OutstandingRunIDs: []string{}}
	addErr := func(err error) {
		report.Status = CleanupIncomplete
		report.Errors = append(report.Errors, err.Error())
		r.logAlways("one-shot cleanup: %v", err)
	}

	budgetCtx, cancelBudget := context.WithDeadline(context.Background(), time.Now().Add(shutdownBudget))
	defer cancelBudget()

	if srv != nil {
		if err := srv.Shutdown(budgetCtx); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				_ = srv.Close()
			}
			addErr(fmt.Errorf("shutdown control API: %w", err))
		}
	}
	if manager != nil {
		if err := manager.Stop(budgetCtx); err != nil {
			addErr(fmt.Errorf("workflow shutdown: %w", err))
			report.OutstandingRunIDs = append(report.OutstandingRunIDs, manager.OutstandingRunIDs()...)
		}
	}
	if orch != nil {
		if err := orch.WaitForWorkers(budgetCtx); err != nil {
			addErr(fmt.Errorf("wait for workers: %w", err))
		}
	}
	if orchCancel != nil {
		orchCancel()
	}
	if orch != nil {
		orch.Close()
	}
	sortStrings(report.OutstandingRunIDs)
	return report
}

// writeSummaryJSON emits the final report; false means the summary could not
// be delivered on stdout and the command must fail regardless of the planned
// exit code.
func (r *runner) writeSummaryJSON(summary *Summary) bool {
	encoder := json.NewEncoder(r.stdout)
	if err := encoder.Encode(summary); err != nil {
		r.logAlways("one-shot: write summary to stdout: %v", err)
		return false
	}
	return true
}

func mustMarshal(summary *Summary) []byte {
	data, err := json.Marshal(summary)
	if err != nil {
		return []byte("{}")
	}
	return data
}

func summarySnapshotPath(workflowDir string) string {
	if workflowDir == "" {
		return ""
	}
	return workflowDir + "/workflow.json"
}

func collectDecisionPaths(st *store.Store, snapshot *domain.WorkflowSnapshot) []string {
	paths := []string{}
	for _, taskID := range sortedTaskIDs(snapshot.Tasks) {
		task := snapshot.Tasks[taskID]
		for i := range task.Attempts {
			attempt := &task.Attempts[i]
			if attempt.Verdict == nil {
				continue
			}
			if path, err := st.WorkflowDecisionPath(snapshot.ExecutionID, taskID, attempt.Attempt); err == nil {
				paths = append(paths, path)
			}
		}
	}
	return paths
}

func declaresVerdicts(def *domain.WorkflowDefinition) bool {
	if def == nil {
		return false
	}
	for _, task := range def.Tasks {
		if len(task.Verdicts) > 0 {
			return true
		}
	}
	return false
}

func sortedTaskIDs(tasks map[string]*domain.WorkflowTaskExecution) []string {
	ids := make([]string, 0, len(tasks))
	for taskID := range tasks {
		ids = append(ids, taskID)
	}
	sortStrings(ids)
	return ids
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
