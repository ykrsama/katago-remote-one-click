package links

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestMigrationUsesLegacyToken(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)
	legacy := strings.Repeat("a", 43)
	list, err := store.LoadOrCreate(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Token != legacy || list[0].MaxClients != Unlimited {
		t.Fatalf("migrated links = %+v", list)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(store.Path())
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("links.json mode = %o, want 600", got)
		}
	}
	again, err := store.LoadOrCreate("different")
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 1 || again[0].Token != legacy {
		t.Fatalf("migration ran twice: %+v", again)
	}
}

func TestCreateSetDeleteRotate(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)
	legacy := strings.Repeat("a", 43)
	if _, err := store.LoadOrCreate(legacy); err != nil {
		t.Fatal(err)
	}

	created, err := store.Create(2)
	if err != nil {
		t.Fatal(err)
	}
	if created.MaxClients != 2 || len(created.Token) < 32 {
		t.Fatalf("created = %+v", created)
	}
	list, err := store.Load()
	if err != nil || len(list) != 2 {
		t.Fatalf("links = %+v, err = %v", list, err)
	}

	updated, err := store.SetMaxClients(created.ID[:4], Disabled)
	if err != nil {
		t.Fatal(err)
	}
	if updated.MaxClients != Disabled {
		t.Fatalf("updated = %+v", updated)
	}

	rotated, err := store.RotateToken(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Token == created.Token {
		t.Fatal("token did not rotate")
	}

	deleted, err := store.Delete(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.ID != created.ID {
		t.Fatalf("deleted = %+v", deleted)
	}
	remaining, err := store.Load()
	if err != nil || len(remaining) != 1 || remaining[0].Token != legacy {
		t.Fatalf("remaining = %+v, err = %v", remaining, err)
	}
	if _, err := store.Delete(created.ID); err == nil {
		t.Fatal("deleted link was resolved again")
	}
}

func TestValidateMaxClients(t *testing.T) {
	if err := ValidateMaxClients(-2); err == nil {
		t.Fatal("maxClients=-2 was accepted")
	}
	for _, value := range []int{Unlimited, Disabled, 1, 100} {
		if err := ValidateMaxClients(value); err != nil {
			t.Fatalf("maxClients=%d rejected: %v", value, err)
		}
	}
}

func TestResolveAmbiguousIDPrefix(t *testing.T) {
	list := []Link{
		{ID: "abcd1111", Token: strings.Repeat("b", 43), MaxClients: Unlimited},
		{ID: "abcd2222", Token: strings.Repeat("c", 43), MaxClients: Unlimited},
	}
	if _, err := Resolve(list, "abcd"); err == nil {
		t.Fatal("ambiguous prefix was accepted")
	}
	resolved, err := Resolve(list, "abcd1")
	if err != nil || resolved.ID != "abcd1111" {
		t.Fatalf("resolved = %+v, err = %v", resolved, err)
	}
	byToken, err := Resolve(list, list[1].Token)
	if err != nil || byToken.ID != "abcd2222" {
		t.Fatalf("by token = %+v, err = %v", byToken, err)
	}
}

func TestFindByToken(t *testing.T) {
	token := strings.Repeat("d", 43)
	list := []Link{{ID: "one", Token: token, MaxClients: Unlimited}}
	if link, ok := FindByToken(list, token); !ok || link.ID != "one" {
		t.Fatalf("find = %+v, %v", link, ok)
	}
	if _, ok := FindByToken(list, strings.Repeat("e", 43)); ok {
		t.Fatal("wrong token matched")
	}
}
