package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	quickTunnelURL = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
	// cloudflared logs "Registered tunnel connection ..." in recent releases and
	// "Connection <uuid> registered ..." in older ones.
	tunnelRegistered = regexp.MustCompile(`(?i)(?:\bregistered tunnel connection|connection [0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12} registered)`)
)

type tunnelSpec struct {
	Executable   string
	LocalAddress string
	Timeout      time.Duration
	Named        bool
	Hostname     string
	Token        string
}

type tunnelStartupEvent struct {
	url        string
	registered bool
}

type tunnel struct {
	cmd  *exec.Cmd
	done chan error
	once sync.Once
}

// tunnelCommand builds the cloudflared invocation. A named tunnel receives its
// token through TUNNEL_TOKEN so the secret never shows up in the process argv.
// The argument order matches Cloudflare's container examples:
// `cloudflared tunnel --no-autoupdate run`.
func tunnelCommand(spec tunnelSpec) (args []string, environment []string) {
	if spec.Named {
		return []string{"tunnel", "--no-autoupdate", "run"},
			[]string{"TUNNEL_TOKEN=" + spec.Token}
	}
	return []string{
		"tunnel",
		"--no-autoupdate",
		"--url", "http://" + spec.LocalAddress,
		"--protocol", "http2",
		"--loglevel", "info",
	}, nil
}

func startTunnel(
	ctx context.Context,
	spec tunnelSpec,
	logger *log.Logger,
) (*tunnel, string, error) {
	args, environment := tunnelCommand(spec)
	cmd := exec.CommandContext(ctx, spec.Executable, args...)
	if environment != nil {
		cmd.Env = append(os.Environ(), environment...)
	}
	configureChildProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, "", err
	}
	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("start cloudflared: %w", err)
	}
	t := &tunnel{cmd: cmd, done: make(chan error, 1)}
	go func() {
		err := cmd.Wait()
		t.done <- err
		close(t.done)
	}()

	events := make(chan tunnelStartupEvent, 32)
	tail := &lineTail{limit: 12}
	read := func(input io.Reader) {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 16*1024), 1<<20)
		for scanner.Scan() {
			line := redactSecret(scanner.Text(), spec.Token)
			logger.Printf("cloudflared: %s", line)
			tail.add(line)
			event := tunnelStartupEvent{
				url:        quickTunnelURL.FindString(line),
				registered: tunnelRegistered.MatchString(line),
			}
			if event.url != "" || event.registered {
				select {
				case events <- event:
				default:
				}
			}
		}
	}
	go read(stdout)
	go read(stderr)

	baseURL := ""
	if spec.Named {
		baseURL = "https://" + spec.Hostname
	}
	timer := time.NewTimer(spec.Timeout)
	defer timer.Stop()
	registered := false
	for baseURL == "" || !registered {
		select {
		case event := <-events:
			if event.url != "" {
				baseURL = event.url
			}
			registered = registered || event.registered
		case err := <-t.done:
			if err == nil {
				err = errors.New("cloudflared exited before the tunnel became ready")
			}
			return nil, "", tunnelStartupError(err, tail)
		case <-timer.C:
			t.stop(5 * time.Second)
			return nil, "", tunnelStartupError(
				errors.New("timed out waiting for the secure tunnel to register"),
				tail,
			)
		case <-ctx.Done():
			t.stop(5 * time.Second)
			return nil, "", ctx.Err()
		}
	}
	return t, baseURL, nil
}

func (t *tunnel) stop(timeout time.Duration) {
	if t == nil {
		return
	}
	t.once.Do(func() {
		terminateChild(t.cmd)
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-t.done:
		case <-timer.C:
			killChild(t.cmd)
			<-t.done
		}
	})
}

func redactSecret(line, secret string) string {
	if secret == "" {
		return line
	}
	return strings.ReplaceAll(line, secret, "[redacted]")
}

func tunnelStartupError(err error, tail *lineTail) error {
	detail := strings.ReplaceAll(strings.TrimSpace(tail.String()), "\n", " | ")
	if detail == "" {
		return err
	}
	if len(detail) > 1500 {
		detail = "..." + detail[len(detail)-1500:]
	}
	return fmt.Errorf("%w (%s)", err, detail)
}

// lineTail keeps the most recent cloudflared output lines for error reports.
type lineTail struct {
	mu    sync.Mutex
	lines []string
	limit int
}

func (t *lineTail) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, line)
	if len(t.lines) > t.limit {
		t.lines = append([]string(nil), t.lines[len(t.lines)-t.limit:]...)
	}
}

func (t *lineTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}
