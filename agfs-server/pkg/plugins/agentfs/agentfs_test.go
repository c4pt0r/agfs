package agentfs

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/c4pt0r/agfs/agfs-server/pkg/filesystem"
)

const testSkill = `---
name: news
description: News digests and headlines.
ttl: 30m
keywords: [news, headlines]
---

# Skill: news

## Instructions
1. Fetch fresh news via http_get.

## Файлы
- headlines.txt — top-10 headlines
- digest.txt: extended digest
`

func TestParseSkill(t *testing.T) {
	sk, err := ParseSkill("news.md", []byte(testSkill))
	if err != nil {
		t.Fatalf("ParseSkill failed: %v", err)
	}
	if sk.Name != "news" || sk.FileName != "news.md" {
		t.Errorf("unexpected identity: %+v", sk)
	}
	if sk.TTL != 30*time.Minute {
		t.Errorf("ttl = %v, want 30m", sk.TTL)
	}
	if len(sk.Keywords) != 2 {
		t.Errorf("keywords = %v, want 2 entries", sk.Keywords)
	}
	if sk.Description != "News digests and headlines." {
		t.Errorf("description = %q", sk.Description)
	}
	wantFiles := map[string]string{
		"headlines.txt": "top-10 headlines",
		"digest.txt":    "extended digest",
	}
	if len(sk.Files) != len(wantFiles) {
		t.Fatalf("files = %+v, want %d entries", sk.Files, len(wantFiles))
	}
	for _, f := range sk.Files {
		if wantFiles[f.Name] != f.Description {
			t.Errorf("file %q desc = %q, want %q", f.Name, f.Description, wantFiles[f.Name])
		}
	}
}

func TestParseSkillErrors(t *testing.T) {
	if _, err := ParseSkill("other.md", []byte(testSkill)); err == nil {
		t.Error("mismatched file name must fail")
	}
	noFrontmatter := "just text\n"
	if _, err := ParseSkill("x.md", []byte(noFrontmatter)); err == nil {
		t.Error("missing frontmatter must fail")
	}
	if _, err := ParseSkill("x.md", []byte("---\nname: Bad Name\n---\n")); err == nil {
		t.Error("invalid skill name must fail")
	}
	if _, err := ParseSkill("x.md", []byte("---\nname: ok\nttl: bogus\n---\n")); err == nil {
		t.Error("invalid ttl must fail")
	}
}

func TestCache(t *testing.T) {
	c := NewCache()
	c.Set("/news/a.txt", []byte("hello"), 50*time.Millisecond)
	if data, ok := c.Get("/news/a.txt"); !ok || string(data) != "hello" {
		t.Fatalf("Get after Set failed: %v %v", ok, string(data))
	}
	if _, ok := c.GetInfo("/news/a.txt"); !ok {
		t.Fatal("GetInfo must hit")
	}
	c.InvalidatePath("/news/a.txt")
	if _, ok := c.Get("/news/a.txt"); ok {
		t.Fatal("entry must be gone after InvalidatePath")
	}
	c.Set("/p/x", []byte("1"), time.Minute)
	c.Set("/p/y", []byte("2"), time.Minute)
	c.Set("/q/z", []byte("3"), time.Minute)
	if n := c.InvalidatePrefix("/p/"); n != 2 {
		t.Fatalf("InvalidatePrefix(/p/) = %d, want 2", n)
	}
	if _, ok := c.Get("/q/z"); !ok {
		t.Fatal("/q/z must survive")
	}
	time.Sleep(60 * time.Millisecond)
	if _, ok := c.Get("/news/a.txt"); ok {
		t.Fatal("expired entry must not be returned (re-set above was invalidated)")
	}
	c.Set("/t/x", []byte("9"), 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	if _, ok := c.Get("/t/x"); ok {
		t.Fatal("expired entry must not be returned")
	}
	if infos := c.ListPrefix("/t/"); len(infos) != 0 {
		t.Fatal("ListPrefix must skip expired entries")
	}
}

func newTestFS(t *testing.T) *AgentFS {
	t.Helper()
	return NewAgentFS(NewSkillRegistry(), NewAgent(AgentConfig{}), DefaultSkillTTL, "usage text")
}

func TestAgentFSSkillLifecycle(t *testing.T) {
	fs := newTestFS(t)

	if _, err := fs.Write("/.skills/news.md", []byte(testSkill), -1, filesystem.WriteFlagCreate|filesystem.WriteFlagTruncate); err != nil {
		t.Fatalf("Write skill failed: %v", err)
	}
	if fs.reg.Len() != 1 {
		t.Fatalf("registry len = %d, want 1", fs.reg.Len())
	}

	entries, err := fs.ReadDir("/")
	if err != nil {
		t.Fatalf("ReadDir / failed: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	if len(names) != 3 {
		t.Errorf("root entries = %v, want [README.md .skills news]", names)
	}

	files, err := fs.ReadDir("/.skills")
	if err != nil {
		t.Fatalf("ReadDir /.skills failed: %v", err)
	}
	if len(files) != 1 || files[0].Name != "news.md" {
		t.Errorf(".skills entries = %+v, want news.md", files)
	}

	raw, err := fs.Read("/.skills/news.md", 0, -1)
	if err != nil && err != io.EOF {
		t.Fatalf("Read skill failed: %v", err)
	}
	if !strings.Contains(string(raw), "name: news") {
		t.Error("raw skill content mismatch")
	}

	info, err := fs.Stat("/news")
	if err != nil || !info.IsDir {
		t.Fatalf("Stat /news = %+v err=%v, want dir", info, err)
	}

	dirEntries, err := fs.ReadDir("/news")
	if err != nil {
		t.Fatalf("ReadDir /news failed: %v", err)
	}
	declared := map[string]bool{}
	for _, e := range dirEntries {
		declared[e.Name] = true
	}
	for _, want := range []string{"README.md", "headlines.txt", "digest.txt"} {
		if !declared[want] {
			t.Errorf("ReadDir /news missing %q, got %v", want, declared)
		}
	}

	rm, err := fs.Read("/news/README.md", 0, -1)
	if err != nil && err != io.EOF {
		t.Fatalf("Read skill README failed: %v", err)
	}
	if !strings.Contains(string(rm), "# Skill: news") {
		t.Error("skill README content mismatch")
	}

	if err := fs.Remove("/.skills/news.md"); err != nil {
		t.Fatalf("Remove skill failed: %v", err)
	}
	if fs.reg.Len() != 0 {
		t.Fatalf("registry len after Remove = %d, want 0", fs.reg.Len())
	}
	var after *filesystem.FileInfo
	after, err = fs.Stat("/news")
	if err != nil {
		t.Fatalf("Stat /news after removal failed: %v", err)
	}
	if after.IsDir {
		t.Error("Stat /news after skill removal must not report a directory")
	}
}

func TestAgentFSReadOnly(t *testing.T) {
	fs := newTestFS(t)
	if _, err := fs.Write("/news/x.txt", []byte("data"), -1, filesystem.WriteFlagCreate); err == nil {
		t.Error("Write outside /.skills must fail")
	}
	if _, err := fs.Write("/.skills/skill.txt", []byte("data"), -1, filesystem.WriteFlagCreate); err == nil {
		t.Error("Write of non-.md skill file must fail")
	}
	if err := fs.Mkdir("/tmp", 0755); err == nil {
		t.Error("Mkdir must fail")
	}
	if _, err := fs.OpenWrite("/news/x.txt"); err == nil {
		t.Error("OpenWrite must fail")
	}
	if err := fs.Truncate("/whatever", 0); err != nil {
		t.Error("Truncate must be a no-op")
	}
	if _, err := fs.Read("/", 0, -1); err != errIsDir {
		t.Errorf("Read / = %v, want errIsDir", err)
	}
}
