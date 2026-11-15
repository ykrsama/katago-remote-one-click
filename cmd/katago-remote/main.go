package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	appconfig "github.com/ykrsama/katago-remote-one-click/internal/config"
	"github.com/ykrsama/katago-remote-one-click/internal/links"
	"github.com/ykrsama/katago-remote-one-click/internal/service"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error / 错误: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}
	switch args[0] {
	case "init":
		return initCommand(args[1:], stdout)
	case "run":
		return runCommand(args[1:], stdout)
	case "start":
		home, _, err := homeFlags("start", args[1:])
		if err != nil {
			return err
		}
		return service.StartDaemon(home, version, stdout)
	case "stop":
		home, _, err := homeFlags("stop", args[1:])
		if err != nil {
			return err
		}
		return service.Stop(home, stdout)
	case "restart":
		home, _, err := homeFlags("restart", args[1:])
		if err != nil {
			return err
		}
		if err := service.Stop(home, io.Discard); err != nil {
			return err
		}
		return service.StartDaemon(home, version, stdout)
	case "status":
		return statusCommand(args[1:], stdout)
	case "show":
		home, _, err := homeFlags("show", args[1:])
		if err != nil {
			return err
		}
		return service.Show(home, stdout)
	case "reset-link":
		home, _, err := homeFlags("reset-link", args[1:])
		if err != nil {
			return err
		}
		return service.ResetLink(home, version, stdout)
	case "link":
		return linkCommand(args[1:], stdout)
	case "doctor":
		return doctorCommand(args[1:], stdout)
	case "logs":
		return logsCommand(args[1:], stdout)
	case "version", "--version", "-v":
		_, _ = fmt.Fprintf(stdout, "katago-remote %s\n", version)
		return nil
	case "help", "--help", "-h":
		printUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func initCommand(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	home := flags.String("home", "", "installation directory")
	katago := flags.String("katago", "", "KataGo executable")
	model := flags.String("model", "", "neural network model")
	humanModel := flags.String("human-model", "", "KataGo Human SL model for humanSLProfile queries")
	analysisConfig := flags.String("config", "", "analysis config")
	cloudflared := flags.String("cloudflared", "", "cloudflared executable")
	listen := flags.String("listen", "", "local listen address (default 127.0.0.1:8765)")
	maxVisits := flags.Int64("max-visits", 0, "maximum visits accepted per request")
	tunnelMode := flags.String("tunnel-mode", "", "tunnel mode: quick or named")
	tunnelHostname := flags.String("tunnel-hostname", "", "fixed public hostname for a named tunnel")
	tunnelTokenFile := flags.String("tunnel-token-file", "", "file holding the named tunnel token ('-' reads stdin)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	resolved, err := resolveHome(*home)
	if err != nil {
		return err
	}
	tunnelToken, err := readTunnelToken(*tunnelTokenFile)
	if err != nil {
		return err
	}
	mode := *tunnelMode
	if mode == "" && (*tunnelHostname != "" || tunnelToken != "") {
		mode = appconfig.TunnelModeNamed
	}
	if err := service.Initialize(resolved, service.InitOptions{
		KataGo:         *katago,
		Model:          *model,
		HumanModel:     *humanModel,
		Config:         *analysisConfig,
		Cloudflared:    *cloudflared,
		Listen:         *listen,
		MaxVisits:      *maxVisits,
		TunnelMode:     mode,
		TunnelHostname: *tunnelHostname,
		TunnelToken:    tunnelToken,
	}); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(output, "Configured / 配置完成: %s\n", resolved)
	return nil
}

// readTunnelToken prefers an explicit file, then KATAGO_REMOTE_TUNNEL_TOKEN.
func readTunnelToken(path string) (string, error) {
	if path == "" {
		return strings.TrimSpace(os.Getenv("KATAGO_REMOTE_TUNNEL_TOKEN")), nil
	}
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(os.Stdin, appconfig.MaxTunnelTokenBytes+1))
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("read tunnel token: %w", err)
	}
	token := strings.TrimSpace(string(data))
	return strings.Trim(token, `"'`), nil
}

func linkCommand(args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: katago-remote link create|list|set|delete [flags]")
	}
	sub := args[0]
	home := ""
	maxClients := links.Unlimited
	maxSet := false
	positional := make([]string, 0, 2)

	rest := args[1:]
	for index := 0; index < len(rest); index++ {
		argument := rest[index]
		switch {
		case argument == "--home" || argument == "-home":
			if index+1 >= len(rest) {
				return errors.New("--home requires a value")
			}
			home = rest[index+1]
			index++
		case strings.HasPrefix(argument, "--home="):
			home = strings.TrimPrefix(argument, "--home=")
		case strings.HasPrefix(argument, "-home="):
			home = strings.TrimPrefix(argument, "-home=")
		case argument == "--max-clients" || argument == "-max-clients":
			if index+1 >= len(rest) {
				return errors.New("--max-clients requires a value")
			}
			value, err := strconv.Atoi(rest[index+1])
			if err != nil {
				return fmt.Errorf("invalid --max-clients: %w", err)
			}
			maxClients = value
			maxSet = true
			index++
		case strings.HasPrefix(argument, "--max-clients="):
			value, err := strconv.Atoi(strings.TrimPrefix(argument, "--max-clients="))
			if err != nil {
				return fmt.Errorf("invalid --max-clients: %w", err)
			}
			maxClients = value
			maxSet = true
		case strings.HasPrefix(argument, "-max-clients="):
			value, err := strconv.Atoi(strings.TrimPrefix(argument, "-max-clients="))
			if err != nil {
				return fmt.Errorf("invalid --max-clients: %w", err)
			}
			maxClients = value
			maxSet = true
		default:
			positional = append(positional, argument)
		}
	}
	resolved, err := resolveHome(home)
	if err != nil {
		return err
	}
	switch sub {
	case "create":
		if len(positional) != 0 {
			return errors.New("usage: katago-remote link create [--max-clients N]")
		}
		return service.CreateLink(resolved, maxClients, output)
	case "list":
		if len(positional) != 0 {
			return errors.New("usage: katago-remote link list")
		}
		return service.ListLinks(resolved, output)
	case "set":
		if !maxSet {
			return errors.New("link set requires --max-clients")
		}
		if len(positional) != 1 {
			return errors.New("usage: katago-remote link set <id> --max-clients N")
		}
		return service.SetLinkMaxClients(resolved, positional[0], maxClients, output)
	case "delete":
		if len(positional) != 1 {
			return errors.New("usage: katago-remote link delete <id>")
		}
		return service.DeleteLink(resolved, positional[0], output)
	default:
		return fmt.Errorf("unknown link command %q", sub)
	}
}

func runCommand(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	home := flags.String("home", "", "installation directory")
	serviceVersion := flags.String("version", version, "service version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	resolved, err := resolveHome(*home)
	if err != nil {
		return err
	}
	logger := log.New(output, time.Now().Format("2006-01-02")+" ", log.Ldate|log.Ltime|log.LUTC)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return service.Run(ctx, resolved, *serviceVersion, logger)
}

func statusCommand(args []string, output io.Writer) error {
	home, _, err := homeFlags("status", args)
	if err != nil {
		return err
	}
	status, err := service.ReadStatus(home)
	if err != nil {
		return err
	}
	if !status.Running {
		_, _ = fmt.Fprintln(output, "Stopped / 未运行")
		return nil
	}
	if status.Connection == nil {
		_, _ = fmt.Fprintf(output, "Starting / 正在启动 (pid=%d)\n", status.PID)
		return nil
	}
	_, _ = fmt.Fprintf(output, "Running / 运行中 (pid=%d)\n", status.PID)
	_, _ = fmt.Fprintln(output, "Run `katago-remote show` to display the private links and QR code.")
	_, _ = fmt.Fprintln(output, "运行 `katago-remote show` 查看私密链接和二维码。")
	return nil
}

func doctorCommand(args []string, output io.Writer) error {
	home, _, err := homeFlags("doctor", args)
	if err != nil {
		return err
	}
	checks := service.Doctor(context.Background(), home)
	failed := 0
	for _, check := range checks {
		mark := "PASS"
		if !check.OK {
			mark = "FAIL"
			failed++
		}
		message := strings.ReplaceAll(check.Message, "\n", " | ")
		_, _ = fmt.Fprintf(output, "[%s] %s: %s\n", mark, check.Name, message)
	}
	if failed > 0 {
		return fmt.Errorf("%d environment check(s) failed", failed)
	}
	return nil
}

func logsCommand(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("logs", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	home := flags.String("home", "", "installation directory")
	lines := flags.Int("lines", 80, "number of lines")
	if err := flags.Parse(args); err != nil {
		return err
	}
	resolved, err := resolveHome(*home)
	if err != nil {
		return err
	}
	return service.TailLog(resolved, output, *lines)
}

func homeFlags(name string, args []string) (string, []string, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	home := flags.String("home", "", "installation directory")
	if err := flags.Parse(args); err != nil {
		return "", nil, err
	}
	resolved, err := resolveHome(*home)
	return resolved, flags.Args(), err
}

func resolveHome(value string) (string, error) {
	if strings.TrimSpace(value) != "" {
		return filepath.Abs(value)
	}
	return appconfig.DefaultHome()
}

func printUsage(output io.Writer) {
	_, _ = fmt.Fprintln(output, `KataGo Remote One-Click / KataGo 远程算力一键部署

Commands:
  init        Save paths and create a private access token
  start       Start KataGo and create a secure WSS link
  stop        Stop the service safely
  restart     Restart while keeping the same private token and hostname
  status      Show whether the service is running
  show        Show all private links and the QR code
  reset-link  Rotate the first private link and restart
  link        Manage private links (create, list, set, delete)
  doctor      Check GPU, KataGo, model, and tunnel prerequisites
  logs        Show recent service logs
  version     Show the installed version

Private links:
  katago-remote link create [--max-clients N]   # -1 unlimited, 0 disabled
  katago-remote link list
  katago-remote link set <id> --max-clients N
  katago-remote link delete <id>

Named tunnels use a fixed Cloudflare hostname:
  katago-remote init --tunnel-mode named --tunnel-hostname katago.example.org \
    --tunnel-token-file /path/to/tunnel-token

Run the one-line installer from the project README for normal setup.`)
}
