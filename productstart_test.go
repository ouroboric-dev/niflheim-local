package niflheim

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The stand-ins are this test binary run again. A stand-in script sets YGG_STANDIN and executes the binary; TestMain
// sees the variable, records what the part was given, opens its port and waits to be stopped. The tests need bash and
// nothing else, so they never skip.
func TestMain(m *testing.M) {
	if name := os.Getenv("YGG_STANDIN"); name != "" {
		standIn(name)
		return
	}
	os.Exit(m.Run())
}

func standIn(name string) {
	rec := os.Getenv("YGG_REC")
	args := os.Args[1:]
	write := func(file, text string) {
		f, err := os.OpenFile(filepath.Join(rec, file), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			_, _ = f.WriteString(text)
			_ = f.Close()
		}
	}
	write(name+".calls", "x\n")
	write(name+".args", strings.Join(args, "\n")+"\n")
	write(name+".pid", strconv.Itoa(os.Getpid())+"\n")
	flagValue := func(prefix string) string {
		for i, a := range args {
			if v, ok := strings.CutPrefix(a, prefix+"="); ok {
				return v
			}
			if a == prefix && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	var addr string
	switch name {
	case "initdb":
		_ = os.MkdirAll(flagValue("-D"), 0o755)
		_ = os.WriteFile(filepath.Join(flagValue("-D"), "PG_VERSION"), []byte("18\n"), 0o644)
		return
	case "createdb":
		return
	case "postgres":
		addr = net.JoinHostPort(flagValue("-h"), flagValue("-p"))
	case "weed":
		id := flagValue("-s3.config")
		if st, err := os.Stat(id); err == nil {
			data, _ := os.ReadFile(id)
			write("weed.identity", fmt.Sprintf("%04o\n%s\n", st.Mode().Perm(), data))
		}
		addr = net.JoinHostPort(flagValue("-ip.bind"), flagValue("-s3.port"))
	case "api", "web":
		write(name+".env", strings.Join(os.Environ(), "\n")+"\n")
		addr = net.JoinHostPort(os.Getenv("HOST"), os.Getenv("PORT"))
	}
	if os.Getenv("YGG_DEAD_PART") == name {
		time.Sleep(time.Minute)
		return
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stand-in:", err)
		os.Exit(1)
	}
	defer l.Close()
	// Bash starts a background job with SIGINT ignored, and Postgres installs its own handler for it. So does this.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-time.After(time.Minute):
	}
}

type app struct {
	t     *testing.T
	dir   string
	bin   string
	rec   string
	ports map[string]int
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func writeExec(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
		t.Fatal(err)
	}
}

// newApp makes an /app-like folder with the script, the stand-in tools first on the PATH and stand-in leaves.
// release says whether the leaves have a release (.ygg/release/repos/<leaf>/run.sh) or only repos/<leaf>/run.sh.
func newApp(t *testing.T, release bool) *app {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a := &app{t: t, dir: t.TempDir(), rec: t.TempDir(), ports: map[string]int{}}
	a.bin = filepath.Join(t.TempDir(), "bin")
	script, err := os.ReadFile("templates/product/start.sh")
	if err != nil {
		t.Fatal(err)
	}
	writeExec(t, filepath.Join(a.dir, "start.sh"), string(script))
	for _, tool := range []string{"initdb", "postgres", "createdb", "weed"} {
		writeExec(t, filepath.Join(a.bin, tool), fmt.Sprintf("#!/bin/bash\nexec env YGG_STANDIN=%s %q \"$@\"\n", tool, self))
	}
	for _, leaf := range []string{"api", "web"} {
		path := filepath.Join(a.dir, "repos", leaf, "run.sh")
		which := "leaf"
		if release {
			path = filepath.Join(a.dir, ".ygg", "release", "repos", leaf, "run.sh")
			which = "release"
		}
		writeExec(t, path, fmt.Sprintf("#!/bin/bash\nexec env YGG_STANDIN=%s YGG_WHICH=%s %q\n", leaf, which, self))
	}
	if !release {
		return a
	}
	// The leaves exist as folders too, as in a project.
	for _, leaf := range []string{"api", "web"} {
		_ = os.MkdirAll(filepath.Join(a.dir, "repos", leaf), 0o755)
	}
	return a
}

type run struct {
	cmd  *exec.Cmd
	out  *bytes.Buffer
	done chan error
}

func (a *app) start(extraEnv ...string) *run {
	a.t.Helper()
	for _, n := range []string{"pg", "s3", "api", "web"} {
		a.ports[n] = freePort(a.t)
	}
	cmd := exec.Command("bash", filepath.Join(a.dir, "start.sh"))
	cmd.Env = append(os.Environ(),
		"PATH="+a.bin+":"+os.Getenv("PATH"),
		"YGG_REC="+a.rec,
		"YGG_PG_PORT="+strconv.Itoa(a.ports["pg"]),
		"YGG_S3_PORT="+strconv.Itoa(a.ports["s3"]),
		"YGG_API_PORT="+strconv.Itoa(a.ports["api"]),
		"YGG_WEB_PORT="+strconv.Itoa(a.ports["web"]),
		"YGG_START_WAIT=20",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	r := &run{cmd: cmd, out: &bytes.Buffer{}, done: make(chan error, 1)}
	cmd.Stdout, cmd.Stderr = r.out, r.out
	if err := cmd.Start(); err != nil {
		a.t.Fatal(err)
	}
	go func() { r.done <- cmd.Wait() }()
	a.t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGKILL)
		for _, name := range []string{"postgres", "weed", "api", "web"} {
			for _, pid := range a.pids(name) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	return r
}

func (a *app) pids(name string) []int {
	data, _ := os.ReadFile(filepath.Join(a.rec, name+".pid"))
	var pids []int
	for _, f := range strings.Fields(string(data)) {
		if n, err := strconv.Atoi(f); err == nil {
			pids = append(pids, n)
		}
	}
	return pids
}

func alive(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	s := string(data)
	state := strings.Fields(s[strings.LastIndex(s, ")")+1:])[0]
	return state != "Z"
}

// waitReady waits until the web port answers, which is the last part to start.
func (a *app) waitReady(r *run) {
	a.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-r.done:
			a.t.Fatalf("the script ended before it was ready: %v\n%s", err, r.out)
		default:
		}
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", a.ports["web"]), time.Second); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	a.t.Fatalf("the script never opened the web port\n%s", r.out)
}

// stop sends TERM to the script and waits for it to end.
func (a *app) stop(r *run) {
	a.t.Helper()
	_ = r.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-r.done:
	case <-time.After(30 * time.Second):
		a.t.Fatalf("the script did not end after TERM\n%s", r.out)
	}
}

func (a *app) wait(r *run) error {
	a.t.Helper()
	select {
	case err := <-r.done:
		return err
	case <-time.After(60 * time.Second):
		a.t.Fatalf("the script did not end\n%s", r.out)
		return nil
	}
}

func (a *app) allStopped(names ...string) {
	a.t.Helper()
	for _, name := range names {
		pids := a.pids(name)
		if len(pids) == 0 {
			a.t.Errorf("%s never ran", name)
		}
		for _, pid := range pids {
			deadline := time.Now().Add(10 * time.Second)
			for alive(pid) && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			if alive(pid) {
				a.t.Errorf("%s (pid %d) is still running", name, pid)
			}
		}
	}
}

func (a *app) args(name string) []string {
	data, _ := os.ReadFile(filepath.Join(a.rec, name+".args"))
	return strings.Fields(string(data))
}

func (a *app) calls(name string) int {
	data, _ := os.ReadFile(filepath.Join(a.rec, name+".calls"))
	return strings.Count(string(data), "x")
}

// env returns the environment the part recorded at its last start.
func (a *app) env(name string) map[string]string {
	data, err := os.ReadFile(filepath.Join(a.rec, name+".env"))
	if err != nil {
		a.t.Fatalf("%s recorded no environment: %v", name, err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	return env
}

// flag returns the value of -name=value or -name value in a recorded argument list.
func flag(args []string, name string) string {
	for i, a := range args {
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func (a *app) clearRecords(names ...string) {
	for _, n := range names {
		for _, ext := range []string{".args", ".env", ".pid", ".identity"} {
			_ = os.Remove(filepath.Join(a.rec, n+ext))
		}
	}
}

func TestProductStart(t *testing.T) {
	t.Run("gives each part its names", func(t *testing.T) {
		a := newApp(t, true)
		r := a.start()
		a.waitReady(r)
		data := filepath.Join(a.dir, ".data")

		api, web := a.env("api"), a.env("web")
		apiPort, webPort := strconv.Itoa(a.ports["api"]), strconv.Itoa(a.ports["web"])
		for name, env := range map[string]map[string]string{"api": api, "web": web} {
			if env["YGG_WHICH"] != "release" {
				t.Errorf("%s ran from %q; want its release", name, env["YGG_WHICH"])
			}
		}
		for k, want := range map[string]string{
			"HOST": "0.0.0.0", "PORT": webPort, "API_URL": "http://127.0.0.1:" + apiPort,
			"S3_ENDPOINT": "http://127.0.0.1:" + strconv.Itoa(a.ports["s3"]), "S3_BUCKET": "ygg-store",
		} {
			if web[k] != want {
				t.Errorf("web %s = %q; want %q", k, web[k], want)
			}
		}
		for k, want := range map[string]string{
			"HOST": "127.0.0.1", "PORT": apiPort, "S3_REGION": "us-east-1", "S3_BUCKET": "ygg-store",
			"S3_ENDPOINT":  "http://127.0.0.1:" + strconv.Itoa(a.ports["s3"]),
			"DATABASE_URL": fmt.Sprintf("postgres://postgres@127.0.0.1:%d/app?sslmode=disable", a.ports["pg"]),
		} {
			if api[k] != want {
				t.Errorf("api %s = %q; want %q", k, api[k], want)
			}
		}
		if d := api["DATA_DIR"]; !strings.HasPrefix(d, data+"/") {
			t.Errorf("api DATA_DIR = %q; want a folder under %s", d, data)
		}
		if api["S3_ACCESS_KEY_ID"] == "" || len(api["S3_SECRET_ACCESS_KEY"]) < 32 {
			t.Errorf("api key pair = %q / (%d chars); want a fresh pair", api["S3_ACCESS_KEY_ID"], len(api["S3_SECRET_ACCESS_KEY"]))
		}

		pg := a.args("postgres")
		if d := flag(pg, "-D"); !strings.HasPrefix(d, data+"/") {
			t.Errorf("postgres data folder = %q; want one under %s", d, data)
		}
		if flag(pg, "-h") != "127.0.0.1" || flag(pg, "-p") != strconv.Itoa(a.ports["pg"]) {
			t.Errorf("postgres listens on %q:%q; want 127.0.0.1:%d", flag(pg, "-h"), flag(pg, "-p"), a.ports["pg"])
		}
		if k := flag(pg, "-k"); k == "" || strings.HasPrefix(k, data) {
			t.Errorf("postgres socket folder = %q; want a temporary folder outside .data", k)
		}
		weed := a.args("weed")
		if d := flag(weed, "-dir"); !strings.HasPrefix(d, data+"/") {
			t.Errorf("weed data folder = %q; want one under %s", d, data)
		}
		for _, f := range []string{"-ip", "-ip.bind"} {
			if flag(weed, f) != "127.0.0.1" {
				t.Errorf("weed %s = %q; want 127.0.0.1", f, flag(weed, f))
			}
		}
		if !slices.Contains(weed, "-s3") {
			t.Errorf("weed args %v lack -s3", weed)
		}
		if flag(weed, "-s3.port") != strconv.Itoa(a.ports["s3"]) {
			t.Errorf("weed S3 port = %q; want %d", flag(weed, "-s3.port"), a.ports["s3"])
		}
		taken := []string{"8080", "3000", apiPort, webPort, strconv.Itoa(a.ports["pg"])}
		for _, f := range []string{"-master.port", "-volume.port", "-filer.port"} {
			if v := flag(weed, f); v == "" || slices.Contains(taken, v) {
				t.Errorf("weed %s = %q; want a port away from %v", f, v, taken)
			}
		}

		ident, _ := os.ReadFile(filepath.Join(a.rec, "weed.identity"))
		mode, body, _ := strings.Cut(string(ident), "\n")
		if mode != "0600" {
			t.Errorf("identity file mode = %q; want 0600", mode)
		}
		if !strings.Contains(body, api["S3_SECRET_ACCESS_KEY"]) || !strings.Contains(body, api["S3_ACCESS_KEY_ID"]) {
			t.Errorf("identity file %q lacks the key pair given to the API", body)
		}
		a.stop(r)
		if bytes.Contains(r.out.Bytes(), []byte(api["S3_SECRET_ACCESS_KEY"])) || bytes.Contains(r.out.Bytes(), []byte(api["S3_ACCESS_KEY_ID"])) {
			t.Errorf("the script printed the key pair:\n%s", r.out)
		}
	})

	t.Run("a second start finds the data and makes a new key", func(t *testing.T) {
		a := newApp(t, true)
		r := a.start()
		a.waitReady(r)
		first := a.env("api")
		a.stop(r)
		a.allStopped("postgres", "weed", "api", "web")
		a.clearRecords("postgres", "weed", "api", "web")

		r = a.start()
		a.waitReady(r)
		second := a.env("api")
		a.stop(r)
		if n := a.calls("initdb"); n != 1 {
			t.Errorf("initdb ran %d times over two starts; want 1", n)
		}
		if n := a.calls("createdb"); n != 1 {
			t.Errorf("createdb ran %d times over two starts; want 1", n)
		}
		if first["S3_SECRET_ACCESS_KEY"] == second["S3_SECRET_ACCESS_KEY"] || first["S3_ACCESS_KEY_ID"] == second["S3_ACCESS_KEY_ID"] {
			t.Errorf("the key pair is the same at both starts")
		}
		if first["DATA_DIR"] != second["DATA_DIR"] {
			t.Errorf("DATA_DIR %q then %q; want the same folder", first["DATA_DIR"], second["DATA_DIR"])
		}
	})

	t.Run("without a release it starts the leaves", func(t *testing.T) {
		a := newApp(t, false)
		r := a.start()
		a.waitReady(r)
		for _, name := range []string{"api", "web"} {
			if w := a.env(name)["YGG_WHICH"]; w != "leaf" {
				t.Errorf("%s ran from %q; want repos/%s/run.sh", name, w, name)
			}
		}
		a.stop(r)
	})

	t.Run("without repos/web it exits and names the path", func(t *testing.T) {
		a := newApp(t, true)
		if err := os.RemoveAll(filepath.Join(a.dir, "repos", "web")); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Join(a.dir, ".ygg", "release", "repos", "web")); err != nil {
			t.Fatal(err)
		}
		r := a.start()
		if err := a.wait(r); err == nil {
			t.Error("the script exited 0; want non-zero")
		}
		if !strings.Contains(r.out.String(), "repos/web") {
			t.Errorf("output %q does not name repos/web", r.out)
		}
		if n := a.calls("postgres"); n != 0 {
			t.Errorf("postgres started %d times before the leaves were checked", n)
		}
	})

	t.Run("a part that does not open its port stops the others", func(t *testing.T) {
		a := newApp(t, true)
		r := a.start("YGG_DEAD_PART=web", "YGG_START_WAIT=15")
		if err := a.wait(r); err == nil {
			t.Error("the script exited 0; want non-zero")
		}
		if !strings.Contains(r.out.String(), "web") {
			t.Errorf("output %q does not name the web part", r.out)
		}
		a.allStopped("postgres", "weed", "api", "web")
	})

	t.Run("TERM stops every part", func(t *testing.T) {
		a := newApp(t, true)
		r := a.start()
		a.waitReady(r)
		a.stop(r)
		a.allStopped("postgres", "weed", "api", "web")
	})

	t.Run("it refuses to run as root", func(t *testing.T) {
		a := newApp(t, true)
		writeExec(t, filepath.Join(a.bin, "id"), "#!/bin/bash\necho 0\n")
		r := a.start()
		if err := a.wait(r); err == nil {
			t.Error("the script exited 0 as root; want non-zero")
		}
		if !strings.Contains(r.out.String(), "root") {
			t.Errorf("output %q does not say why it refuses", r.out)
		}
		if n := a.calls("postgres") + a.calls("initdb"); n != 0 {
			t.Errorf("%d parts started as root", n)
		}
	})
}
