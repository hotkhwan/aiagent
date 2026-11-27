package world

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Made-up values: the tests check where a value may and may not go.
const (
	fakeDev  = "EXAMPLE-dev-value-0001"
	fakeUAT  = "EXAMPLE-uat-value-0002"
	fakeProd = "EXAMPLE-prod-value-0003"
)

func envAdmin(t *testing.T) (*env, string) {
	t.Helper()
	e := newEnv(t)
	admin, err := e.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	return e, admin
}

// next lets the following write through the one-per-second limit.
func (e *env) next() { e.svc.lastWrite = e.svc.lastWrite.Add(-writeSpacing) }

func TestEnvironmentValuesAreWriteOnly(t *testing.T) {
	e, admin := envAdmin(t)
	code, m, body := e.do(t, "POST", apiPrefix+"/environments", admin, `{"name":"netra-dev","tier":"dev","note":"Netra on the dev cluster"}`)
	if code != 200 {
		t.Fatalf("create = %d %s", code, body)
	}
	id, _ := m["details"].(map[string]any)["environment"].(map[string]any)["id"].(string)
	if id == "" {
		t.Fatalf("no id: %s", body)
	}
	e.next()
	if code, _, body := e.do(t, "POST", apiPrefix+"/environments/"+id+"/vars", admin, `{"key":"DATABASE_URL","description":"dev database","value":"`+fakeDev+`"}`); code != 200 || strings.Contains(body, fakeDev) {
		t.Fatalf("set var = %d, answer holds the value: %s", code, body)
	}
	e.next()
	// a variable may be described before it has a value
	if code, _, body := e.do(t, "POST", apiPrefix+"/environments/"+id+"/vars", admin, `{"key":"API_KEY","description":"later"}`); code != 200 {
		t.Fatalf("describe var = %d %s", code, body)
	}
	_, _, body = e.do(t, "GET", apiPrefix+"/environments", admin, "")
	if strings.Contains(body, fakeDev) || !strings.Contains(body, "DATABASE_URL") || !strings.Contains(body, "netra-dev") {
		t.Fatalf("list shows the value or misses the entry: %s", body)
	}
	list, err := e.svc.Environments()
	if err != nil || len(list) != 1 || len(list[0].Vars) != 2 {
		t.Fatalf("list = %+v %v", list, err)
	}
	for _, v := range list[0].Vars {
		if want := v.Key == "DATABASE_URL"; v.Set != want {
			t.Fatalf("%s set = %v, want %v", v.Key, v.Set, want)
		}
	}
	// the metadata file never holds a value; the values file is 0600
	if b, _ := os.ReadFile(filepath.Join(e.store.dir, envFile)); strings.Contains(string(b), fakeDev) {
		t.Fatal("the value is in the metadata file")
	}
	for _, f := range []string{envFile, envSecrets} {
		fi, err := os.Stat(filepath.Join(e.store.dir, f))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v %v", f, fi, err)
		}
	}
	// an edit without a value keeps the stored one
	e.next()
	if code, _, body := e.do(t, "POST", apiPrefix+"/environments/"+id+"/vars", admin, `{"key":"DATABASE_URL","description":"the dev database"}`); code != 200 {
		t.Fatalf("edit var = %d %s", code, body)
	}
	if got := e.svc.envValue(t, id, "DATABASE_URL"); got != fakeDev {
		t.Fatalf("an empty value changed the stored one: %q", got)
	}
	e.next()
	if code, _, body := e.do(t, "POST", apiPrefix+"/environments/"+id+"/vars/DATABASE_URL/remove", admin, `{}`); code != 200 {
		t.Fatalf("remove var = %d %s", code, body)
	}
	if b, _ := os.ReadFile(filepath.Join(e.store.dir, envSecrets)); strings.Contains(string(b), fakeDev) {
		t.Fatal("the value outlived its variable")
	}
	e.next()
	if code, _, body := e.do(t, "POST", apiPrefix+"/environments/"+id+"/edit", admin, `{"name":"netra-dev2","tier":"uat","note":"moved"}`); code != 200 || !strings.Contains(body, "netra-dev2") {
		t.Fatalf("edit = %d %s", code, body)
	}
	e.next()
	if code, _, body := e.do(t, "POST", apiPrefix+"/environments/"+id+"/remove", admin, `{}`); code != 200 {
		t.Fatalf("remove = %d %s", code, body)
	}
	if list, _ := e.svc.Environments(); len(list) != 0 {
		t.Fatalf("still listed: %+v", list)
	}
	// every write was audited, and no value reached the audit log
	b, _ := os.ReadFile(e.auditFP)
	for _, rpc := range []string{"aiagent.environmentAdd", "aiagent.environmentVar", "aiagent.environmentVarRemove", "aiagent.environmentEdit", "aiagent.environmentRemove"} {
		if !strings.Contains(string(b), rpc) {
			t.Errorf("audit has no %s", rpc)
		}
	}
	if strings.Contains(string(b), fakeDev) {
		t.Fatal("a value reached the audit log")
	}
}

// envValue reads one stored value, for the tests only.
func (s *Service) envValue(t *testing.T, id, key string) string {
	t.Helper()
	var sec map[string]map[string]string
	if err := readJSON(filepath.Join(s.Store.dir, envSecrets), &sec); err != nil {
		t.Fatal(err)
	}
	return sec[id][key]
}

func TestEnvironmentsAreAdminOnly(t *testing.T) {
	e, admin := envAdmin(t)
	_, user, _ := e.store.AddUser("anna", "")
	en, err := e.svc.AddEnvironment("netra-dev", "dev", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range [][3]string{
		{"GET", "/environments", ""},
		{"POST", "/environments", `{"name":"x-dev","tier":"dev"}`},
		{"POST", "/environments/" + en.ID + "/edit", `{"name":"x-dev","tier":"dev"}`},
		{"POST", "/environments/" + en.ID + "/remove", `{}`},
		{"POST", "/environments/" + en.ID + "/vars", `{"key":"A_B","value":"` + fakeDev + `"}`},
		{"POST", "/environments/" + en.ID + "/vars/A_B/remove", `{}`},
	} {
		if code, _, _ := e.do(t, r[0], apiPrefix+r[1], user, r[2]); code != 403 {
			t.Errorf("a user token got %d for %s %s", code, r[0], r[1])
		}
	}
	for _, re := range append(userRoutes["GET"], userRoutes["POST"]...) {
		if re.MatchString("/environments") || re.MatchString("/environments/"+en.ID+"/vars") {
			t.Fatalf("userRoutes lets a user reach the environments: %s", re)
		}
	}
	if code, _, _ := e.do(t, "GET", apiPrefix+"/environments", admin, ""); code != 200 {
		t.Fatalf("admin list = %d", code)
	}
	if !localPath("/environments") || !localPath("/environments/"+en.ID+"/vars") {
		t.Fatal("an admin looking at a room would send the environments to that room")
	}
}

// While the admin looks at a room, a value saved then is kept here, never
// sent to that room's instance.
func TestEnvironmentValueNeverForwardedToARoom(t *testing.T) {
	e, _, o := osUserEnv(t)
	admin, err := e.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = e.store.AddUser("vibe-a", "vibe-a")
	var seen []string
	instance(t, o, "vibe-a", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(`{"code":"SUCCESS","details":{}}`))
	})
	req := httptest.NewRequest("POST", apiPrefix+"/environments", strings.NewReader(`{"name":"netra-dev","tier":"dev"}`))
	req.Header.Set(TokenHeader, admin)
	req.Header.Set(AsHeader, "vibe-a")
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 0 || resp.StatusCode != 200 {
		t.Fatalf("status %d, sent to the room: %v", resp.StatusCode, seen)
	}
}

func TestEnvironmentFieldsAreChecked(t *testing.T) {
	e, _ := envAdmin(t)
	for _, c := range [][3]string{
		{"Netra-Dev", "dev", "upper case"},
		{"../etc", "dev", "a path"},
		{"netra dev", "dev", "a space"},
		{"netra-dev", "staging", "an unknown tier"},
		{"netra-dev", "dev", strings.Repeat("n", 301)},
		{"netra-dev", "dev", "two\nlines"},
	} {
		if _, err := e.svc.AddEnvironment(c[0], c[1], c[2]); err == nil {
			t.Errorf("accepted %q %q %q", c[0], c[1], c[2])
		}
	}
	en, err := e.svc.AddEnvironment("netra-dev", "dev", "ok")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddEnvironment("netra-dev", "uat", "again"); err == nil {
		t.Error("the same name twice")
	}
	for _, c := range [][2]string{
		{"lower_case", fakeDev},
		{"1ABC", fakeDev},
		{"A-B", fakeDev},
		{"PATH", fakeDev},
		{"LD_PRELOAD", fakeDev},
		{"AIAGENT_TOKEN", fakeDev},
		{"GOOD_KEY", "two\nlines"},
		{"GOOD_KEY", "nul\x00byte"},
		{"GOOD_KEY", strings.Repeat("v", maxEnvValue+1)},
	} {
		if _, err := e.svc.SetEnvVar(en.ID, c[0], "", c[1]); err == nil {
			t.Errorf("accepted key %q value len %d", c[0], len(c[1]))
		}
	}
	if _, err := e.svc.SetEnvVar(en.ID, "GOOD_KEY", strings.Repeat("d", 201), fakeDev); err == nil {
		t.Error("a long description was accepted")
	}
	if _, err := e.svc.SetEnvVar("nope", "GOOD_KEY", "", fakeDev); err == nil {
		t.Error("a variable on an unknown environment")
	}
	for i := 0; i < maxEnvVars; i++ {
		if _, err := e.svc.SetEnvVar(en.ID, "K"+strings.Repeat("A", i+1), "", ""); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := e.svc.SetEnvVar(en.ID, "ONE_MORE", "", ""); err == nil {
		t.Error("more variables than the limit")
	}
}

// roomsEnv makes rooms with duties and three environments with values.
func roomsEnv(t *testing.T) (*env, map[string]string) {
	t.Helper()
	e, _ := envAdmin(t)
	e.svc.Duties = &RoomDuties{Path: filepath.Join(t.TempDir(), "room-duties.conf")}
	for room, duty := range map[string]string{"fe-a": DutyFrontend, "be-a": DutyBackend, "qa-a": DutyTester, "ad-a": DutyAdmin, "no-duty": ""} {
		if _, _, err := e.store.AddUser(room, room); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.Duties.Set(room, duty); err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]string{}
	for name, v := range map[string][2]string{"netra-dev": {"dev", fakeDev}, "netra-uat": {"uat", fakeUAT}, "netra-prod": {"prod", fakeProd}} {
		en, err := e.svc.AddEnvironment(name, v[0], "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.SetEnvVar(en.ID, "DATABASE_URL", "", v[1]); err != nil {
			t.Fatal(err)
		}
		ids[name] = en.ID
	}
	return e, ids
}

func TestRoomsGetDevAndUATNeverProd(t *testing.T) {
	e, _ := roomsEnv(t)
	plan, err := e.svc.RoomEnvPlan()
	if err != nil {
		t.Fatal(err)
	}
	for _, room := range []string{"fe-a", "be-a", "ad-a"} {
		files := plan[room]
		if len(files) != 2 || !strings.Contains(string(files["netra-dev.env"]), fakeDev) || !strings.Contains(string(files["netra-uat.env"]), fakeUAT) {
			t.Errorf("%s gets %v", room, keys(files))
		}
	}
	for _, room := range []string{"qa-a", "no-duty"} {
		if files, ok := plan[room]; !ok || len(files) != 0 {
			t.Errorf("%s gets %v (listed %v); a room with nothing must still be synced so old files go", room, keys(files), ok)
		}
	}
	for room, files := range plan {
		for name, b := range files {
			if strings.Contains(string(b), fakeProd) || strings.Contains(name, "prod") {
				t.Fatalf("prod reached %s in %s", room, name)
			}
		}
	}
	// fullstack (roomduties.go may add it) works on both sides
	if p := envPlan(nil, nil, []string{"fs-a"}, map[string]string{"fs-a": "fullstack"}); len(p["fs-a"]) != 0 {
		t.Fatal("a plan with no environments delivered something")
	}
	if !deliverTo("fullstack", "dev") || deliverTo("fullstack", "prod") || deliverTo(DutyTester, "dev") || deliverTo("", "uat") || deliverTo(DutyAdmin, "prod") {
		t.Fatal("deliverTo is not dev+uat for builders and admins, nothing else")
	}
}

func TestDutyChangeOrProdTierTakesTheFilesAway(t *testing.T) {
	e, ids := roomsEnv(t)
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "aiagent", "env")
	sync := func() {
		t.Helper()
		plan, err := e.svc.RoomEnvPlan()
		if err != nil {
			t.Fatal(err)
		}
		if err := WriteRoomEnvDir(dir, plan["fe-a"]); err != nil {
			t.Fatal(err)
		}
	}
	sync()
	if names := dirNames(t, dir); strings.Join(names, ",") != "netra-dev.env,netra-uat.env" {
		t.Fatalf("delivered %v", names)
	}
	// uat moved to prod: gone on the next sync
	if _, err := e.svc.EditEnvironment(ids["netra-uat"], "netra-uat", "prod", ""); err != nil {
		t.Fatal(err)
	}
	sync()
	if names := dirNames(t, dir); strings.Join(names, ",") != "netra-dev.env" {
		t.Fatalf("after prod: %v", names)
	}
	// the variable removed: the file stays, without it
	if err := e.svc.RemoveEnvVar(ids["netra-dev"], "DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	sync()
	if b, _ := os.ReadFile(filepath.Join(dir, "netra-dev.env")); strings.Contains(string(b), fakeDev) {
		t.Fatal("a removed variable stayed in the room")
	}
	// the room became a tester: nothing left
	if err := e.svc.Duties.Set("fe-a", DutyTester); err != nil {
		t.Fatal(err)
	}
	sync()
	if names := dirNames(t, dir); len(names) != 0 {
		t.Fatalf("a tester keeps %v", names)
	}
}

func TestRoomEnvFilesAreTheRoomsOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "env")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// files the room left: an old delivery goes, anything else stays
	_ = os.WriteFile(filepath.Join(dir, "old-dev.env"), []byte("X=1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o644)
	content := envFileContent(Environment{Name: "netra-dev", Tier: "dev", Vars: []EnvVar{{Key: "QUOTED"}, {Key: "UNSET"}}}, map[string]string{"QUOTED": "it's $HOME `x` \"y\""})
	if err := WriteRoomEnvDir(dir, map[string][]byte{"netra-dev.env": content}); err != nil {
		t.Fatal(err)
	}
	if names := dirNames(t, dir); strings.Join(names, ",") != "netra-dev.env,notes.txt" {
		t.Fatalf("left %v", names)
	}
	fi, _ := os.Stat(dir)
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", fi.Mode())
	}
	fi, _ = os.Stat(filepath.Join(dir, "netra-dev.env"))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", fi.Mode())
	}
	if strings.Contains(string(content), "UNSET") {
		t.Fatal("a variable with no value was written")
	}
	// the file is sourced by a shell; the value must come back exactly
	out, err := exec.Command("/bin/sh", "-c", `. "$1"; printf %s "$QUOTED"`, "sh", filepath.Join(dir, "netra-dev.env")).Output()
	if err != nil || string(out) != "it's $HOME `x` \"y\"" {
		t.Fatalf("sourced %q %v", out, err)
	}
	for _, bad := range []string{"../x.env", "x.txt", ".env", "A.env"} {
		if err := WriteRoomEnvDir(dir, map[string][]byte{bad: []byte("x")}); err == nil {
			t.Errorf("wrote %q", bad)
		}
	}
}

func TestEnvironmentWriteStartsTheSync(t *testing.T) {
	e, r, _ := osUserEnv(t)
	admin, err := e.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	if code, _, body := e.do(t, "POST", apiPrefix+"/environments", admin, `{"name":"netra-dev","tier":"dev"}`); code != 200 {
		t.Fatalf("create = %d %s", code, body)
	}
	if strings.Join(r.calls, "|") != "systemctl start --no-block "+roomEnvUnit {
		t.Fatalf("calls = %v", r.calls)
	}
}

func TestRoomEnvListingHasNoValues(t *testing.T) {
	e, _ := roomsEnv(t)
	b, err := e.svc.RoomEnvListing()
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, v := range []string{fakeDev, fakeUAT, fakeProd} {
		if strings.Contains(s, v) {
			t.Fatal("the listing holds a value")
		}
	}
	for _, want := range []string{"netra-dev=dev\n", "netra-uat=uat\n", "netra-prod=prod\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("listing misses %q: %s", want, s)
		}
	}
}

// The sync reaches every room, one failing room does not stop the others, and
// an error names the room, never a value.
func TestSyncRoomEnvReachesEveryRoom(t *testing.T) {
	e, _ := roomsEnv(t)
	got := map[string]int{}
	errs := e.svc.SyncRoomEnv(func(room string, files map[string][]byte) error {
		got[room] = len(files)
		if room == "be-a" {
			return os.ErrPermission
		}
		return nil
	})
	if len(got) != 5 || got["fe-a"] != 2 || got["qa-a"] != 0 || got["no-duty"] != 0 {
		t.Fatalf("delivered %v", got)
	}
	if len(errs) != 1 || !strings.HasPrefix(errs[0].Error(), "be-a: ") || strings.Contains(errs[0].Error(), "EXAMPLE") {
		t.Fatalf("errs = %v", errs)
	}
}

// A revoked room (its token is being removed) keeps nothing.
func TestARemovedRoomKeepsNothing(t *testing.T) {
	e, _ := roomsEnv(t)
	st, _ := e.store.Load()
	for _, u := range st.Users {
		if u.OSUser == "fe-a" {
			if _, err := e.store.BlockUser(u.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	plan, err := e.svc.RoomEnvPlan()
	if err != nil {
		t.Fatal(err)
	}
	if files, ok := plan["fe-a"]; !ok || len(files) != 0 {
		t.Fatalf("a room being removed gets %v", keys(files))
	}
}

func TestEnvironmentBodyIsLimited(t *testing.T) {
	e, admin := envAdmin(t)
	big := `{"name":"netra-dev","tier":"dev","note":"` + strings.Repeat("x", maxEnvBody) + `"}`
	if code, _, _ := e.do(t, "POST", apiPrefix+"/environments", admin, big); code != 413 {
		t.Fatalf("a huge body = %d", code)
	}
}

// Setting a room's duty starts the sync, so a room that became a tester
// loses its files without waiting for the timer.
func TestDutyChangeStartsTheSync(t *testing.T) {
	e, r, _ := osUserEnv(t)
	e.svc.Duties = &RoomDuties{Path: filepath.Join(t.TempDir(), "room-duties.conf")}
	admin, err := e.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	u, _, err := e.store.AddUser("vibe-b", "vibe-b")
	if err != nil {
		t.Fatal(err)
	}
	if code, _, body := e.do(t, "POST", apiPrefix+"/tokens/"+u.ID+"/duty", admin, `{"duty":"tester"}`); code != 200 {
		t.Fatalf("set duty = %d %s", code, body)
	}
	if !strings.Contains(strings.Join(r.calls, "|"), "systemctl start --no-block "+roomEnvUnit) {
		t.Fatalf("calls = %v", r.calls)
	}
}

func keys(m map[string][]byte) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}
