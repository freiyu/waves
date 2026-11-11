package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"waves/internal/store"
)

// validatorRuns tracks the validator processes Waves started, so approval
// requests from their process trees can be refused (AH-5).
type validatorRuns struct {
	mu   sync.Mutex
	pids map[int]bool
	jobs chan string // phase ids whose command validators must run
}

// startValidation creates the runs for a phase that entered Validation and
// queues its command validators. Checklists wait for the human to confirm.
func (s *Service) startValidation(ctx context.Context, t *Tx, phaseID string) error {
	rows, err := t.QueryContext(ctx, `SELECT id FROM validators WHERE phase_id = ? ORDER BY position`, phaseID)
	if err != nil {
		return wrap(err, "read validators")
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if _, err := t.ExecContext(ctx, `INSERT INTO validation_runs(id, validator_id, phase_id, status) VALUES (?,?,?, 'pending')`, NewID("vrn"), id, phaseID); err != nil {
			return wrap(err, "create validation run")
		}
	}
	t.afterCommit(func() { s.queueValidation(phaseID) })
	return nil
}

func (s *Service) queueValidation(phaseID string) {
	select {
	case s.vr.jobs <- phaseID:
	default:
		s.log.Error("validation queue full; the phase stays in Validation until the service restarts", "phase", phaseID)
	}
}

// validationLoop runs queued validations one phase at a time.
func (s *Service) validationLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case phase := <-s.vr.jobs:
			if err := s.RunValidation(ctx, phase); err != nil && !errors.Is(err, context.Canceled) {
				s.log.Error("validation", "phase", phase, "err", err)
			}
		}
	}
}

// RunValidation runs the pending command validators of a phase in the
// project folder, then builds the review and asks for Phase Completion.
func (s *Service) RunValidation(ctx context.Context, phaseID string) error {
	var root, project string
	err := s.db.R.QueryRowContext(ctx, `SELECT pr.path, pr.id FROM phases p JOIN workflow_versions v ON v.id = p.workflow_version_id JOIN workflows w ON w.id = v.workflow_id JOIN projects pr ON pr.id = w.project_id
		WHERE p.id = ? AND p.state = 'Validation' AND v.state = 'approved'`, phaseID).Scan(&root, &project)
	if err != nil {
		return wrap(err, "read phase for validation")
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT r.id, v.command, v.timeout_s FROM validation_runs r JOIN validators v ON v.id = r.validator_id
		WHERE r.phase_id = ? AND r.status = 'pending' AND v.type = 'command' ORDER BY v.position`, phaseID)
	if err != nil {
		return wrap(err, "read pending runs")
	}
	type job struct {
		id, cmd string
		timeout int
	}
	var jobs []job
	for rows.Next() {
		var j job
		rows.Scan(&j.id, &j.cmd, &j.timeout)
		jobs = append(jobs, j)
	}
	rows.Close()
	for _, j := range jobs {
		if err := s.runOne(ctx, project, root, j.id, j.cmd, time.Duration(j.timeout)*time.Second); err != nil {
			return err
		}
	}
	return s.tx(ctx, func(t *Tx) error {
		var st string
		t.QueryRowContext(ctx, `SELECT state FROM phases WHERE id = ?`, phaseID).Scan(&st)
		if st != "Validation" {
			return nil
		}
		if _, err := s.transition(ctx, t, "phase", phaseID, "Awaiting Phase Review", sysActor, "validators recorded", ""); err != nil {
			return err
		}
		h, err := s.subjectHash(ctx, t, "phase_completion", "phase", phaseID)
		if err != nil {
			return err
		}
		_, err = s.requestApproval(ctx, t, "phase_completion", "phase", phaseID, project, "system", h, map[string]any{"phase": phaseID})
		return err
	})
}

// validatorEnv is the environment of validator commands: the service
// environment without agent and Waves secrets.
func validatorEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "CLAUDE_CODE_") || strings.HasPrefix(k, "ANTHROPIC_") || strings.HasPrefix(k, "WAVES_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// runMarker is set in the environment of every validator command.
const runMarker = "WAVES_VALIDATION_RUN="

// stopLeftovers kills the processes of interrupted validator runs: a crashed
// service cannot kill their process groups, so recovery finds them by the
// run marker their environment inherited. It returns how many it stopped.
func stopLeftovers(runIDs []string) int {
	if len(runIDs) == 0 {
		return 0
	}
	want := map[string]bool{}
	for _, id := range runIDs {
		want[runMarker+id] = true
	}
	stopped := 0
	// A few rounds catch children forked while the previous round ran.
	for round := 0; round < 3; round++ {
		found := 0
		procs, _ := os.ReadDir("/proc")
		for _, d := range procs {
			pid, err := strconv.Atoi(d.Name())
			if err != nil || pid == os.Getpid() {
				continue
			}
			b, err := os.ReadFile("/proc/" + d.Name() + "/environ")
			if err != nil {
				continue
			}
			for _, kv := range strings.Split(string(b), "\x00") {
				if want[kv] {
					if syscall.Kill(pid, syscall.SIGKILL) == nil {
						found++
					}
					break
				}
			}
		}
		stopped += found
		if found == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return stopped
}

// runOne runs one approved command with /bin/sh -c in the project folder, in
// its own process group, with a timeout. Output streams to a file.
func (s *Service) runOne(ctx context.Context, project, root, runID, command string, timeout time.Duration) error {
	dir := filepath.Join(s.dirs.Logs(), "validators")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return wrap(err, "create validator log dir")
	}
	outPath := filepath.Join(dir, runID+".log")
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return wrap(err, "open validator log")
	}
	defer out.Close()
	start := s.now()
	if _, err := s.db.W.ExecContext(ctx, `UPDATE validation_runs SET started_at = ?, output_path = ? WHERE id = ?`, s.ts(), outPath, runID); err != nil {
		return wrap(err, "mark run started")
	}
	cmd := exec.Command("/bin/sh", "-c", command)
	// The run marker lets recovery find what a crashed service left running.
	cmd.Dir, cmd.Stdout, cmd.Stderr, cmd.Env = root, out, out, append(validatorEnv(), runMarker+runID)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	status, code := "error", -1
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(out, "waves: could not start: %v\n", err)
	} else {
		pid := cmd.Process.Pid
		s.vr.mu.Lock()
		s.vr.pids[pid] = true
		s.vr.mu.Unlock()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		timer := time.NewTimer(timeout)
		select {
		case err = <-done:
			status, code = "pass", 0
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				status, code = "fail", ee.ExitCode()
			} else if err != nil {
				status = "error"
			}
		case <-timer.C:
			syscall.Kill(-pid, syscall.SIGKILL)
			<-done
			status = "timeout"
			fmt.Fprintf(out, "\nwaves: stopped after %s\n", timeout)
		case <-ctx.Done():
			syscall.Kill(-pid, syscall.SIGKILL)
			<-done
			timer.Stop()
			s.vr.mu.Lock()
			delete(s.vr.pids, pid)
			s.vr.mu.Unlock()
			return ctx.Err() // recovery marks the run as interrupted
		}
		timer.Stop()
		// Kill what the command left behind in its group.
		syscall.Kill(-pid, syscall.SIGKILL)
		s.vr.mu.Lock()
		delete(s.vr.pids, pid)
		s.vr.mu.Unlock()
	}
	return s.tx(ctx, func(t *Tx) error {
		if _, err := t.ExecContext(ctx, `UPDATE validation_runs SET status = ?, exit_code = ?, finished_at = ? WHERE id = ?`, status, code, s.ts(), runID); err != nil {
			return wrap(err, "record validation run")
		}
		return t.audit(ctx, store.AuditRecord{Event: "validator.run", ActorScope: "system", ProjectID: project, Subject: runID,
			Detail: map[string]any{"status": status, "exit_code": code, "duration_s": int(s.now().Sub(start).Seconds())}})
	})
}

// ConfirmChecklistItem records that the human checked one checklist item. A
// confirmation is not an approval, but the phase review counts it as a passed
// validator, so it needs a passphrase step-up (a window covers it): an agent
// holding the operator token must not tick the human's checklist.
func (s *Service) ConfirmChecklistItem(ctx context.Context, p Principal, elevation, phaseID, validatorID string, index int) error {
	if !p.IsOperator() {
		return errWrongScope
	}
	return s.tx(ctx, func(t *Tx) error {
		if err := s.RequireElevation(ctx, t, p, elevation); err != nil {
			return err
		}
		var runID, items, conf, project string
		err := t.QueryRowContext(ctx, `SELECT r.id, v.items_json, r.confirmed_json, w.project_id FROM validation_runs r JOIN validators v ON v.id = r.validator_id
			JOIN phases p ON p.id = r.phase_id JOIN workflow_versions wv ON wv.id = p.workflow_version_id JOIN workflows w ON w.id = wv.workflow_id
			WHERE r.phase_id = ? AND v.id = ? AND v.type = 'checklist' AND p.state IN ('Validation','Awaiting Phase Review') ORDER BY r.rowid DESC LIMIT 1`, phaseID, validatorID).Scan(&runID, &items, &conf, &project)
		if err != nil {
			return errNotFound("checklist item", "")
		}
		var list []string
		var done []int
		json.Unmarshal([]byte(items), &list)
		json.Unmarshal([]byte(conf), &done)
		if index < 0 || index >= len(list) {
			return errNotFound("checklist item", "")
		}
		for _, d := range done {
			if d == index {
				return nil
			}
		}
		done = append(done, index)
		b, _ := json.Marshal(done)
		status := "pending"
		if len(done) == len(list) {
			status = "confirmed"
		}
		if _, err := t.ExecContext(ctx, `UPDATE validation_runs SET confirmed_json = ?, status = ?, finished_at = CASE WHEN ? = 'confirmed' THEN ? ELSE finished_at END WHERE id = ?`, string(b), status, status, s.ts(), runID); err != nil {
			return wrap(err, "confirm item")
		}
		t.publish("phase.changed", map[string]string{"phase_id": phaseID, "project_id": project, "from": "", "to": ""})
		return t.audit(ctx, store.AuditRecord{Event: "validator.confirmed", ActorScope: "human", ActorRef: p.ActorRef(), ProjectID: project, Subject: runID, Detail: map[string]any{"item": index}})
	})
}

// reviewNeedsOverride reports whether a phase has a failed, timed-out,
// errored or unconfirmed validator.
func (s *Service) reviewNeedsOverride(ctx context.Context, q store.Execer, phaseID string) (bool, error) {
	var n int
	// Only the latest run of each validator counts: after a revision the old runs are history.
	// A task the human reopened that then ended Cancelled also needs a written
	// reason: cancelling the redo must not quietly clear the failure that caused it.
	err := q.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM validation_runs r WHERE r.phase_id = ?1 AND r.status NOT IN ('pass','confirmed')
		  AND r.rowid = (SELECT max(x.rowid) FROM validation_runs x WHERE x.validator_id = r.validator_id AND x.phase_id = r.phase_id))
		+ (SELECT count(*) FROM tasks t WHERE t.phase_id = ?1 AND t.state = 'Cancelled'
		  AND EXISTS (SELECT 1 FROM state_transitions s WHERE s.entity_type = 'task' AND s.entity_id = t.id AND s.from_state = 'Done' AND s.to_state = 'Ready'))`, phaseID).Scan(&n)
	return n > 0, wrap(err, "count failed validators")
}

// processGuard refuses an approval request whose client process descends
// from a validator command Waves started (AH-5, best effort on Linux: the
// caller is found through /proc/net/tcp by its loopback source port).
func (s *Service) processGuard(ctx context.Context, p Principal) error {
	s.vr.mu.Lock()
	n := len(s.vr.pids)
	roots := make(map[int]bool, n)
	for k := range s.vr.pids {
		roots[k] = true
	}
	s.vr.mu.Unlock()
	if n == 0 {
		return nil
	}
	pid := peerPID(p.Source)
	for i := 0; pid > 1 && i < 64; i++ {
		if roots[pid] {
			s.auditBypass(ctx, Principal{Kind: KindLease, CredID: "validator", Source: p.Source}, "approval from a validator process")
			return &Error{Status: http.StatusForbidden, Code: "wrong_scope", Message: "Approvals are refused from processes Waves started.", NextStep: "Decide in the dashboard or your own terminal."}
		}
		pid = parentPID(pid)
	}
	return nil
}

// peerPID finds the local process that owns the TCP connection from source
// (host:port on 127.0.0.1). It returns 0 when it cannot tell.
func peerPID(source string) int {
	_, port, ok := strings.Cut(source, ":")
	if !ok {
		return 0
	}
	if i := strings.LastIndex(source, ":"); i >= 0 {
		port = source[i+1:]
	}
	pn, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	want := fmt.Sprintf(":%04X", pn)
	inode := ""
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) > 9 && strings.HasSuffix(fields[1], want) {
				inode = fields[9]
			}
		}
		fh.Close()
		if inode != "" {
			break
		}
	}
	if inode == "" || inode == "0" {
		return 0
	}
	target := "socket:[" + inode + "]"
	procs, _ := os.ReadDir("/proc")
	for _, d := range procs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		fds, err := os.ReadDir("/proc/" + d.Name() + "/fd")
		if err != nil {
			continue
		}
		for _, fd := range fds {
			if l, err := os.Readlink("/proc/" + d.Name() + "/fd/" + fd.Name()); err == nil && l == target {
				return pid
			}
		}
	}
	return 0
}

func parentPID(pid int) int {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	// Fields after the command name, which is in parentheses and may contain spaces.
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 2 {
		return 0
	}
	pp, _ := strconv.Atoi(f[1])
	return pp
}
