package main

// sunshine-ctl — управление выводом при стриме Sunshine.
//
//	status          показать состояние
//	layout          DP-1 как основа (2560x1440@240, 100%), DP-2 зеркалом
//	monitor on|off  включить/выключить физический монитор
//	stream on|off   ручной режим стрима
//	watch           демон: реагирует на подключение/отключение клиента
//
// Схема: рабочий стол живёт на виртуальном DP-1, физический DP-2 его зеркалит.
// Во время стрима DP-2 гасится — DP-1 и трансляция не страдают.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	virtOutput = "DP-1"
	physOutput = "DP-2"
	width      = 2560
	height     = 1440
	refresh    = 239958 // в формате cosmic-randr (Гц * 1000)
	logPath    = "~/.config/sunshine/sunshine.log"
	debounce   = 4 * time.Second
	poll       = 1 * time.Second

	statePath string
)

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}

func initConfig() {
	if v := os.Getenv("SUNSHINE_CTL_VIRT"); v != "" {
		virtOutput = v
	}
	if v := os.Getenv("SUNSHINE_CTL_PHYS"); v != "" {
		physOutput = v
	}
	width = envInt("SUNSHINE_CTL_WIDTH", width)
	height = envInt("SUNSHINE_CTL_HEIGHT", height)
	refresh = envInt("SUNSHINE_CTL_REFRESH", refresh)
	if v := os.Getenv("SUNSHINE_CTL_LOG"); v != "" {
		logPath = v
	}
	if v := os.Getenv("SUNSHINE_CTL_DEBOUNCE_SEC"); v != "" {
		debounce = time.Duration(envInt("SUNSHINE_CTL_DEBOUNCE_SEC", 4)) * time.Second
	}
}

func logf(format string, a ...any) {
	fmt.Printf("%s %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
}

func home() string { h, _ := os.UserHomeDir(); return h }

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home(), p[2:])
	}
	return p
}

func env() []string {
	e := os.Environ()
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		e = append(e, fmt.Sprintf("XDG_RUNTIME_DIR=/run/user/%d", os.Getuid()))
	}
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		e = append(e, "WAYLAND_DISPLAY=wayland-1")
	}
	return e
}

func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = env()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

func drmStatus(output string) string {
	b, err := os.ReadFile(fmt.Sprintf("/sys/class/drm/card1-%s/status", output))
	if err != nil {
		return "absent"
	}
	return strings.TrimSpace(string(b))
}

// forceProbe заставляет ядро перечитать EDID (нужно, если монитор ушёл в standby).
func forceProbe(output string) {
	path := fmt.Sprintf("/sys/class/drm/card1-%s/status", output)
	_ = exec.Command("sudo", "-n", "sh", "-c", fmt.Sprintf("echo detect > %s", path)).Run()
	time.Sleep(500 * time.Millisecond)
}

func monitorOn() error {
	if drmStatus(physOutput) != "connected" {
		forceProbe(physOutput)
	}
	// ждём, пока монитор ответит (его могли только что включить)
	for i := 0; i < 10 && drmStatus(physOutput) != "connected"; i++ {
		time.Sleep(300 * time.Millisecond)
	}
	if drmStatus(physOutput) != "connected" {
		return fmt.Errorf("%s не подключён — монитор выключен?", physOutput)
	}
	if _, err := run("cosmic-randr", "enable", physOutput); err != nil {
		return fmt.Errorf("enable %s: %v", physOutput, err)
	}
	for i := 0; i < 10 && !physEnabled(); i++ {
		time.Sleep(200 * time.Millisecond)
	}
	if !physEnabled() {
		return fmt.Errorf("%s не включился", physOutput)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := run("cosmic-randr", "mirror", physOutput, virtOutput); err != nil {
		return fmt.Errorf("mirror: %v", err)
	}
	if !mirrored() {
		return fmt.Errorf("зеркало %s не применилось", physOutput)
	}
	return nil
}

func monitorOff() error {
	if _, err := run("cosmic-randr", "disable", physOutput); err != nil {
		return fmt.Errorf("disable %s: %v", physOutput, err)
	}
	return nil
}

// applyLayout настраивает DP-1: 2560x1440@240, масштаб 100%, позиция 0,0.
// Масштаб меняется через KDL (в cosmic-randr нет отдельной команды).
func applyLayout() error {
	kdl, err := run("cosmic-randr", "list", "--kdl")
	if err != nil {
		return fmt.Errorf("list --kdl: %v", err)
	}
	out := adjustKDL(kdl, virtOutput, 1.0,
		fmt.Sprintf("mode %d %d %d current=#true", width, height, refresh))
	if out == "" {
		return fmt.Errorf("не нашёл output %s в KDL", virtOutput)
	}
	cmd := exec.Command("cosmic-randr", "kdl")
	cmd.Env = env()
	cmd.Stdin = strings.NewReader(out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kdl: %v %s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

// adjustKDL берёт блок output "<name>" и подменяет scale + текущий mode.
func adjustKDL(kdl, name string, scale float64, modeLine string) string {
	lines := strings.Split(kdl, "\n")
	start := -1
	depth := 0
	for i, l := range lines {
		if start < 0 {
			if strings.HasPrefix(strings.TrimSpace(l), fmt.Sprintf("output \"%s\"", name)) {
				start = i
				depth = strings.Count(l, "{") - strings.Count(l, "}")
			}
			continue
		}
		depth += strings.Count(l, "{") - strings.Count(l, "}")
		trim := strings.TrimSpace(l)
		if strings.HasPrefix(trim, "scale ") {
			indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
			lines[i] = fmt.Sprintf("%sscale %g", indent, scale)
		}
		if strings.HasPrefix(trim, "mode ") && strings.Contains(trim, "current=#true") {
			indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
			lines[i] = indent + modeLine
		}
		if depth <= 0 && i > start {
			break
		}
	}
	if start < 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func readState() string {
	b, _ := os.ReadFile(statePath)
	return strings.TrimSpace(string(b))
}

func writeState(s string) {
	if statePath == "" {
		return
	}
	_ = os.WriteFile(statePath, []byte(s+"\n"), 0o644)
}

func stateDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return fmt.Sprintf("/run/user/%d", os.Getuid())
}

// ---- статус ----

func lastClientEvent() (kind, ts string) {
	path := expand(logPath)
	b, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if strings.Contains(l, "CLIENT CONNECTED") {
			return "connected", extractTS(l)
		}
		if strings.Contains(l, "CLIENT DISCONNECTED") {
			return "disconnected", extractTS(l)
		}
	}
	return "", ""
}

func extractTS(l string) string {
	// [2026-11-11 23:36:15.987]:
	if i := strings.Index(l, "["); i >= 0 {
		if j := strings.Index(l[i:], "]"); j > 0 {
			return l[i+1 : i+j]
		}
	}
	return ""
}

func cmdStatus() {
	fmt.Printf("состояние:   %s\n", orDash(readState()))
	c, ts := lastClientEvent()
	fmt.Printf("клиент:      %s %s\n", orDash(c), ts)
	fmt.Printf("%s: %s\n", virtOutput, drmStatus(virtOutput))
	fmt.Printf("%s: %s\n", physOutput, drmStatus(physOutput))
	out, _ := run("cosmic-randr", "list")
	clean := stripANSI(out)
	for _, line := range strings.Split(clean, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "DP-1 (") || strings.HasPrefix(t, "DP-2 (") {
			fmt.Println(t)
		}
		if strings.Contains(t, "2560x1440 @") && strings.Contains(t, "current") {
			fmt.Printf("  режим: %s\n", t)
		}
		if strings.HasPrefix(t, "Scale:") {
			fmt.Printf("  %s\n", t)
		}
	}
}

func outputBlock(out, name string) string {
	lines := strings.Split(stripANSI(out), "\n")
	start := -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if start < 0 {
			if strings.HasPrefix(t, name+" (") {
				start = i
			}
			continue
		}
		if strings.HasPrefix(t, "DP-") || strings.HasPrefix(t, "HDMI-") {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start >= 0 {
		return strings.Join(lines[start:], "\n")
	}
	return ""
}

func stateStr() string { return readState() }

func physEnabled() bool {
	out, _ := run("cosmic-randr", "list")
	return strings.Contains(outputBlock(out, physOutput), "(enabled)")
}

func mirrored() bool {
	out, _ := run("cosmic-randr", "list")
	return strings.Contains(stripANSI(out), physOutput+" (mirroring")
}

func layoutOK() bool {
	out, _ := run("cosmic-randr", "list")
	b := outputBlock(out, virtOutput)
	return strings.Contains(b, "239.958 Hz (current)") && strings.Contains(b, "Scale: 100%")
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !(s[j] >= 0x40 && s[j] <= 0x7e) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// ---- демон ----

type tailer struct {
	path string
	off  int64
}

func (t *tailer) readNew() string {
	fi, err := os.Stat(t.path)
	if err != nil {
		return ""
	}
	if fi.Size() < t.off {
		t.off = 0
	}
	if fi.Size() == t.off {
		return ""
	}
	f, err := os.Open(t.path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.Seek(t.off, 0); err != nil {
		return ""
	}
	buf := make([]byte, fi.Size()-t.off)
	n, _ := f.Read(buf)
	t.off += int64(n)
	return string(buf[:n])
}

func cmdWatch() {
	path := expand(logPath)
	logf("sunshine-ctl watch: лог %s", path)
	logf("выходы: %s (виртуальный) / %s (физический)", virtOutput, physOutput)

	if err := applyLayout(); err != nil {
		logf("layout: %v", err)
	}

	t := &tailer{path: path}
	if fi, err := os.Stat(path); err == nil {
		t.off = fi.Size()
	}
	writeState("desktop")

	var pendingOff time.Time
	lastReconcile := time.Now()

	for {
		time.Sleep(poll)

		if time.Since(lastReconcile) > 10*time.Second {
			lastReconcile = time.Now()
			if stateStr() == "stream" {
				if physEnabled() {
					logf("сверка: во время стрима гашу %s", physOutput)
					_ = monitorOff()
				}
			} else {
				if drmStatus(physOutput) == "connected" && !mirrored() {
					logf("сверка: возвращаю зеркало %s", physOutput)
					_ = monitorOn()
				}
				if !layoutOK() {
					logf("сверка: восстанавливаю режим %s", virtOutput)
					_ = applyLayout()
				}
			}
		}
		chunk := t.readNew()
		if chunk != "" {
			switch {
			case strings.Contains(chunk, "CLIENT CONNECTED"):
				pendingOff = time.Time{}
				if err := monitorOff(); err != nil {
					logf("monitor off: %v", err)
				} else {
					writeState("stream")
					logf("клиент подключён — монитор выключен")
				}
			case strings.Contains(chunk, "CLIENT DISCONNECTED"):
				pendingOff = time.Now().Add(debounce)
			}
		}
		if !pendingOff.IsZero() && time.Now().After(pendingOff) {
			pendingOff = time.Time{}
			if err := monitorOn(); err != nil {
				logf("monitor on: %v", err)
				writeState("desktop (монитор не вернулся)")
			} else {
				writeState("desktop")
				logf("клиент отключён — монитор включён")
			}
			_ = applyLayout()
		}
	}
}

func main() {
	h := home()
	_ = h
	statePath = filepath.Join(stateDir(), "sunshine-ctl.state")

	initConfig()
	if len(os.Args) < 2 || os.Args[1] == "help" || os.Args[1] == "--help" {
		fmt.Println(`sunshine-ctl — управление выводом при стриме Sunshine

  status          состояние (клиент, выходы, режим, масштаб)
  layout          виртуальный выход как основа, физический зеркалом
  monitor on|off  включить/выключить физический монитор
  stream on|off   ручной режим стрима
  watch           демон: реакция на подключение/отключение клиента

Переменные окружения:
  SUNSHINE_CTL_VIRT=DP-1        виртуальный выход (основа)
  SUNSHINE_CTL_PHYS=DP-2        физический выход (зеркало)
  SUNSHINE_CTL_WIDTH=2560       ширина
  SUNSHINE_CTL_HEIGHT=1440      высота
  SUNSHINE_CTL_REFRESH=239958   частота (Гц * 1000)
  SUNSHINE_CTL_LOG=...          путь к sunshine.log
  SUNSHINE_CTL_DEBOUNCE_SEC=4   задержка на отключение`)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "status":
		cmdStatus()
	case "layout":
		if err := applyLayout(); err != nil {
			logf("ошибка: %v", err)
			os.Exit(1)
		}
		if drmStatus(physOutput) == "connected" {
			_ = monitorOn()
		}
		logf("раскладка применена")
	case "monitor":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "нужно on|off")
			os.Exit(2)
		}
		var err error
		if os.Args[2] == "on" {
			err = monitorOn()
			if err == nil {
				writeState("desktop")
			}
		} else {
			err = monitorOff()
			if err == nil {
				writeState("monitor-off")
			}
		}
		if err != nil {
			logf("ошибка: %v", err)
			os.Exit(1)
		}
	case "stream":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "нужно on|off")
			os.Exit(2)
		}
		if os.Args[2] == "on" {
			if err := monitorOff(); err != nil {
				logf("ошибка: %v", err)
				os.Exit(1)
			}
			writeState("stream")
			logf("режим стрима: монитор выключен")
		} else {
			if err := monitorOn(); err != nil {
				logf("ошибка: %v", err)
				os.Exit(1)
			}
			_ = applyLayout()
			writeState("desktop")
			logf("режим стрима снят: монитор включён")
		}
	case "watch":
		cmdWatch()
	default:
		fmt.Fprintln(os.Stderr, "неизвестная команда")
		os.Exit(2)
	}
}
