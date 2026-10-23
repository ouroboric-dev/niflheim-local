package niflheim

import (
	"regexp"
	"slices"
	"testing"

	"github.com/ouroboric-dev/gungnir"
	"github.com/ouroboric-dev/gungnir/schema"
)

const seaweedFSImage = "docker.io/chrislusf/seaweedfs:4.45@sha256:fc9f76fa993ad69966ffeb2f65d0318fcae39c6f8e20cf68ef7b3a5cb97769e5"

func loadPack(t *testing.T) *schema.Pack {
	t.Helper()
	p, err := schema.LoadPack(".")
	if err != nil {
		t.Fatalf("LoadPack() = %v", err)
	}
	return p
}

func service(t *testing.T, p *schema.Pack, need string) gungnir.Service {
	t.Helper()
	svc, ok := p.Provider(need)
	if !ok {
		t.Fatalf("the pack provides no %s", need)
	}
	return svc
}

func TestThePackProvidesTheNeeds(t *testing.T) {
	p := loadPack(t)
	if p.Name != "niflheim-local" || p.Version != "0.1.0" {
		t.Errorf("name %q, version %q; want niflheim-local 0.1.0", p.Name, p.Version)
	}
	if p.Leaf != nil || p.Sandbox != nil || p.Graph != "" || len(p.Checks) > 0 {
		t.Errorf("a runtime realm has no leaf, sandbox, graph or checks: leaf %v, sandbox %v, graph %q, checks %v",
			p.Leaf, p.Sandbox, p.Graph, p.Checks)
	}
	for _, n := range gungnir.Needs {
		if _, ok := p.Provider(n.Name); !ok {
			t.Errorf("the pack does not provide %s (%s)", n.Name, n.Description)
		}
	}

	pg := service(t, p, "postgres")
	if pg.Name != "postgres" || !slices.Equal(pg.Ports, []int{5432}) ||
		pg.Env["POSTGRES_DB"] != "app" || pg.Env["POSTGRES_HOST_AUTH_METHOD"] != "trust" {
		t.Errorf("postgres service = %+v; want port 5432, POSTGRES_DB app, POSTGRES_HOST_AUTH_METHOD trust", pg)
	}
	s3 := service(t, p, "s3")
	if s3.Name != "s3" || !slices.Equal(s3.Ports, []int{8333}) || !slices.Equal(s3.Command, []string{"server", "-s3", "-dir=/data"}) {
		t.Errorf("s3 service = %+v; want port 8333 and command [server -s3 -dir=/data]", s3)
	}

	digest := regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)
	for _, svc := range p.ServiceList() {
		if !digest.MatchString(svc.Image) {
			t.Errorf("service %s image %q is not pinned by digest", svc.Name, svc.Image)
		}
	}
}

func TestPackPostgresIsEighteen(t *testing.T) {
	svc := service(t, loadPack(t), "postgres")
	if !regexp.MustCompile(`postgres:18\.\d+-\S+@sha256:[0-9a-f]{64}`).MatchString(svc.Image) {
		t.Errorf("postgres image %q; want postgres:18.x-<debian>@sha256:<digest>", svc.Image)
	}
}

func TestPackSeaweedFSIsTheGraderImages(t *testing.T) {
	if svc := service(t, loadPack(t), "s3"); svc.Image != seaweedFSImage {
		t.Errorf("s3 image %q; want the grader image's %q", svc.Image, seaweedFSImage)
	}
}
