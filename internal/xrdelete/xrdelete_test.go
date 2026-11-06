package xrdelete_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stuttgart-things/claims/internal/xrdelete"
)

const xrsDir = "clusters/labda/vsphere/machinery-xrs"
const ordersDir = "clusters/labda/vsphere/machinery-orders-xrs"

func vm(kind, name, file string) string {
	return `---
# a comment that must not matter
apiVersion: resources.stuttgart-things.com/v1alpha1
kind: ` + kind + `
metadata:
  name: ` + name + `
  annotations:
    stuttgart-things.com/source-path: ` + file + `
spec:
  size: small
`
}

func run(name, file, applies string) string {
	return `apiVersion: scheduling.stuttgart-things.com/v1alpha1
kind: ScheduledRun
metadata:
  name: ` + name + `
  labels:
    stuttgart-things.com/run-once: "true"
  annotations:
    stuttgart-things.com/source-path: ` + file + `
    stuttgart-things.com/applies: ` + applies + `
spec: {}
`
}

const kustomization = `---
# resources: MUST STAY THE LAST BLOCK
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - app-dev.yaml
  - ./dev7.yaml # the dev VM
  - dev7-resize.yaml
  - dev8.yaml
`

// Fixture builds the fleet-repo layout the tests (and cmd's tests) use:
// two XRs + a run + kustomization.yaml in machinery-xrs, an orders
// directory without kustomization.yaml, and files that must be refused.
func Fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		xrsDir + "/kustomization.yaml":              kustomization,
		xrsDir + "/app-dev.yaml":                    vm("ClusterStack", "app-dev", xrsDir+"/app-dev.yaml"),
		xrsDir + "/dev7.yaml":                       vm("NativeVsphereVM", "dev7-vm", xrsDir+"/dev7.yaml"),
		xrsDir + "/dev7-resize.yaml":                run("dev7-resize", xrsDir+"/dev7-resize.yaml", "NativeVsphereVM/default/dev7-vm"),
		xrsDir + "/dev8.yaml":                       vm("NativeVsphereVM", "dev8-vm", xrsDir+"/dev8.yaml"),
		xrsDir + "/two.yaml":                        vm("NativeVsphereVM", "a-vm", xrsDir+"/two.yaml") + vm("NativeVsphereVM", "b-vm", xrsDir+"/two.yaml"),
		xrsDir + "/moved.yaml":                      vm("NativeVsphereVM", "moved-vm", xrsDir+"/elsewhere.yaml"),
		xrsDir + "/rbac.yaml":                       "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n",
		xrsDir + "/README.md":                       "# readme\n",
		ordersDir + "/claims-e2e.yaml":              vm("NativeVsphereVM", "claims-e2e-vm", ordersDir+"/claims-e2e.yaml"),
		"clusters/labda/vsphere/machinery/xrs.yaml": vm("NativeVsphereVM", "not-in-xrs", "clusters/labda/vsphere/machinery/xrs.yaml"),
	}
	for p, c := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestScan(t *testing.T) {
	root := Fixture(t)
	claims, err := xrdelete.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range claims {
		got = append(got, c.Kind+" "+c.Namespace+"/"+c.Name+" "+c.File)
	}
	want := []string{
		"NativeVsphereVM default/claims-e2e-vm " + ordersDir + "/claims-e2e.yaml",
		"NativeVsphereVM default/dev7-vm " + xrsDir + "/dev7.yaml",
		"NativeVsphereVM default/dev8-vm " + xrsDir + "/dev8.yaml",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Scan =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestList(t *testing.T) {
	root := Fixture(t)
	xrs, err := xrdelete.List(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, x := range xrs {
		got = append(got, x.Kind+" "+x.Name)
	}
	// every XR of the *-xrs directories except the run-once run; files with
	// two XRs, refused kinds and moved files included; not-in-xrs not.
	want := []string{
		"NativeVsphereVM claims-e2e-vm",
		"ClusterStack app-dev",
		"NativeVsphereVM dev7-vm",
		"NativeVsphereVM dev8-vm",
		"NativeVsphereVM moved-vm",
		"NativeVsphereVM a-vm",
		"NativeVsphereVM b-vm",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("List =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestFind(t *testing.T) {
	root := Fixture(t)
	x, err := xrdelete.Find(root, "", "dev7-vm", "")
	if err != nil || x.File != xrsDir+"/dev7.yaml" || x.Kind != "NativeVsphereVM" {
		t.Fatalf("Find = %+v, %v", x, err)
	}
	if _, err := xrdelete.Find(root, "NativeProxmoxVM", "dev7-vm", ""); err == nil {
		t.Error("want an error for the wrong kind")
	}
	if _, err := xrdelete.Find(root, "", "app-dev", ""); err == nil {
		t.Error("a ClusterStack must not be found")
	}
}

func TestPlanWithRunAndKustomization(t *testing.T) {
	root := Fixture(t)
	r, err := xrdelete.Plan(root, "NativeVsphereVM", "dev7-vm", "default", xrsDir+"/dev7.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.Runs, ",") != xrsDir+"/dev7-resize.yaml" {
		t.Errorf("runs = %v", r.Runs)
	}
	if r.Kustomization != xrsDir+"/kustomization.yaml" {
		t.Errorf("kustomization = %q", r.Kustomization)
	}
	want := strings.Replace(strings.Replace(kustomization, "  - ./dev7.yaml # the dev VM\n", "", 1), "  - dev7-resize.yaml\n", "", 1)
	if r.KustomizationAfter != want {
		t.Errorf("kustomization after:\n%s\nwant:\n%s", r.KustomizationAfter, want)
	}
	if err := xrdelete.Apply(root, r, true); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"dev7.yaml", "dev7-resize.yaml"} {
		if _, err := os.Stat(filepath.Join(root, xrsDir, f)); !os.IsNotExist(err) {
			t.Errorf("%s still there", f)
		}
	}
	got, _ := os.ReadFile(filepath.Join(root, xrsDir, "kustomization.yaml"))
	if string(got) != want {
		t.Errorf("kustomization.yaml on disk:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(root, xrsDir, "dev8.yaml")); err != nil {
		t.Error("the other claim was touched")
	}
}

func TestPlanOrdersDirectory(t *testing.T) {
	root := Fixture(t)
	r, err := xrdelete.Plan(root, "NativeVsphereVM", "claims-e2e-vm", "", ordersDir+"/claims-e2e.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if r.Kustomization != "" || len(r.Runs) != 0 {
		t.Errorf("removal = %+v", r)
	}
	if err := xrdelete.Apply(root, r, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ordersDir, "claims-e2e.yaml")); err != nil {
		t.Error("Apply(removeFiles=false) removed the file")
	}
}

func TestPlanRefusals(t *testing.T) {
	root := Fixture(t)
	dup := Fixture(t)
	_ = os.WriteFile(filepath.Join(dup, xrsDir, "kustomization.yaml"), []byte(kustomization+"  - dev8.yaml\n"), 0o644)

	cases := []struct {
		name, root, kind, xr, path, want string
	}{
		{"two XRs in the file", root, "NativeVsphereVM", "a-vm", xrsDir + "/two.yaml", "holds 2 XRs"},
		{"source-path is another file", root, "NativeVsphereVM", "moved-vm", xrsDir + "/moved.yaml", "not the file's own path"},
		{"ClusterStack", root, "ClusterStack", "app-dev", xrsDir + "/app-dev.yaml", "not deleted through this path"},
		{"wrong name", root, "NativeVsphereVM", "other-vm", xrsDir + "/dev8.yaml", "not NativeVsphereVM/default/other-vm"},
		{"not in an *-xrs directory", root, "NativeVsphereVM", "not-in-xrs", "clusters/labda/vsphere/machinery/xrs.yaml", "not in an *-xrs directory"},
		{"path traversal", root, "NativeVsphereVM", "x", xrsDir + "/../machinery-xrs/dev8.yaml", "repository-relative"},
		{"absolute path", root, "NativeVsphereVM", "x", "/" + xrsDir + "/dev8.yaml", "repository-relative"},
		{"kustomization itself", root, "NativeVsphereVM", "x", xrsDir + "/kustomization.yaml", "kustomization.yaml"},
		{"missing file", root, "NativeVsphereVM", "gone-vm", xrsDir + "/gone.yaml", "does not exist"},
		{"invalid name", root, "NativeVsphereVM", "Bad_Name", xrsDir + "/dev8.yaml", "not a valid object name"},
		{"listed twice", dup, "NativeVsphereVM", "dev8-vm", xrsDir + "/dev8.yaml", "lists dev8.yaml 2 times"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := xrdelete.Plan(tc.root, tc.kind, tc.xr, "default", tc.path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestConvention(t *testing.T) {
	root := Fixture(t)
	r, err := xrdelete.Plan(root, "NativeVsphereVM", "dev7-vm", "default", xrsDir+"/dev7.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c := xrdelete.ConventionFor(r, "jdoe", "project ended")
	if c.Branch != "delete-claim/dev7-vm" || c.Title != "Delete claim: NativeVsphereVM/dev7-vm" ||
		c.Commit != "Delete claim NativeVsphereVM/dev7-vm" || c.Label != "delete-claim" {
		t.Errorf("convention = %+v", c)
	}
	for _, want := range []string{"| Kind | `NativeVsphereVM` |", "| Name | `default/dev7-vm` |", "| File | `" + xrsDir + "/dev7.yaml` |",
		"| Requested by | jdoe |", "**Reason:** project ended", "dev7-resize.yaml", "delete-claim-xr.yaml"} {
		if !strings.Contains(c.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, c.Body)
		}
	}
	if !strings.Contains(xrdelete.ConventionFor(r, "", "").Body, "| Requested by | n/a |") {
		t.Error("empty requested-by is not n/a")
	}
}
