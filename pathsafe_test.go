package main

// 路径安全判据（2026-11-04）。
//
// ## 这些判据钉的是两个实测漏洞
//
// ① 路径穿越：handlePluginUninstall(name="..") ⇒ Join(skillsDir,"..") == data_dir
//    ⇒ os.RemoveAll 清空整个数据目录。实测复现：data_dir 剩余条目 0。
//
// ② ZIP Slip：extractZIP 里 Join(dest, f.Name)，条目 "../../pwned.txt"
//    写到 dest 之外。实测复现：文件落在 skills 根外。
//
// ## 为什么判据要钉「行为」而不是「函数存在」
//
// 只测 checkSafeName(...) != nil 是**弱**的：把调用点删掉，函数还在，
// 判据仍然绿。所以下面每一条都走**真实入口**
// （handlePluginUninstall / reloadPlugin / extractArchive），
// 断言的是"根外的东西有没有被动过"。
//
// 变异验证（实测，逐项做过）：
//   · 把 handlePluginUninstall 里的 resolveUnderRoot 换回 filepath.Join
//     ⇒ TestUninstallRejectsTraversal 红
//   · 把 extractZIP 的 safeArchiveTarget 换回 filepath.Join
//     ⇒ TestExtractZipRejectsTraversal 红
//   · 把 safeArchiveTarget 里"含 .. 段"的检查删掉（只留前缀校验）
//     ⇒ TestExtractZipRejectsTraversal **仍红**（前缀校验兜底），
//        但 TestSafeArchiveTargetReasons 会红 —— 它钉的是拒绝**原因**，
//        保证日志说的是真话而不是笼统的"越界"。

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── ① 路径穿越 ──────────────────────────────────────────────

// 判据：uninstall 收到 ".." 时，**整个数据目录必须还在**。
//
// 这是本次最严重的缺陷：data_dir 下有 memory/documents/media/config.db。
func TestUninstallRejectsTraversal(t *testing.T) {
	dataDir := t.TempDir()
	skillsDir := filepath.Join(dataDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	// 造几个"绝不能被删"的兄弟目录（真实布局）
	keep := []string{"memory", "documents", "media"}
	for _, d := range keep {
		full := filepath.Join(dataDir, d)
		os.MkdirAll(full, 0755)
		os.WriteFile(filepath.Join(full, "payload"), []byte("important"), 0644)
	}

	p := New("clawhubadapter", skillsDir)
	for _, evil := range []string{"..", "../", "../..", ".", "a/../..", "/etc"} {
		res, _ := p.handlePluginUninstall(map[string]interface{}{"name": evil})
		// 工具层必须**明确拒绝**，不能静默成功
		if m, ok := res.(map[string]interface{}); ok {
			if _, hasErr := m["isError"]; !hasErr {
				t.Errorf("name=%q 应被拒绝，实际返回成功: %v", evil, m)
			}
		}
		// 无论返回什么，兄弟目录必须原封不动
		for _, d := range keep {
			if _, err := os.Stat(filepath.Join(dataDir, d, "payload")); err != nil {
				t.Fatalf("name=%q ⇒ 数据目录里的 %s 被删掉了（%v）", evil, d, err)
			}
		}
	}
}

// 判据：reload 也必须拒绝越界名字（它是同一条拼路径的三处之一）。
func TestReloadRejectsTraversal(t *testing.T) {
	dataDir := t.TempDir()
	skillsDir := filepath.Join(dataDir, "skills")
	os.MkdirAll(skillsDir, 0755)

	p := New("clawhubadapter", skillsDir)
	for _, evil := range []string{"..", "../memory", "/etc"} {
		if err := p.reloadPlugin(evil); err == nil {
			t.Errorf("reloadPlugin(%q) 应报错", evil)
		} else if !strings.Contains(err.Error(), "非法") && !strings.Contains(err.Error(), "越出") {
			// 报错要说明是**拒绝**，而不是"目录不存在"——
			// 后者会让读者以为是路径写错了，而非被安全策略挡住。
			t.Errorf("reloadPlugin(%q) 的报错应说明被拒绝，实际: %v", evil, err)
		}
	}
}

// ── ② ZIP Slip ──────────────────────────────────────────────

// 判据：含 "../" 条目的 zip 不得写到 dest 之外。
func TestExtractZipRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "skills", "evil")
	os.MkdirAll(dest, 0755)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("../../pwned.txt")
	w.Write([]byte("escaped"))
	zw.Close()

	if err := extractArchive(buf.Bytes(), dest); err == nil {
		t.Fatal("含 ../ 的 zip 必须解压失败（静默成功 = 任意路径写文件）")
	}
	if _, err := os.Stat(filepath.Join(root, "pwned.txt")); err == nil {
		t.Fatalf("ZIP Slip 成立：文件写到了目标目录之外")
	}
}

// 判据：含 "../" 条目的 tar.gz 同样不得越界。
func TestExtractTarRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "skills", "evil")
	os.MkdirAll(dest, 0755)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	body := []byte("escaped")
	tw.WriteHeader(&tar.Header{Name: "../../pwned2.txt", Mode: 0644, Size: int64(len(body))})
	tw.Write(body)
	tw.Close()
	gw.Close()

	if err := extractArchive(buf.Bytes(), dest); err == nil {
		t.Fatal("含 ../ 的 tar 必须解压失败")
	}
	if _, err := os.Stat(filepath.Join(root, "pwned2.txt")); err == nil {
		t.Fatal("tar 越界成立：文件写到了目标目录之外")
	}
}

// 判据：**正常**归档必须还能解开（别把功能一起挡死）。
//
// 上面几条都断言"必须失败"，如果实现改成"无条件拒绝"它们也都绿 ——
// 所以必须有一条正面判据钉住可用性。
func TestExtractZipAllowsNormalEntries(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "skills", "ok")
	os.MkdirAll(dest, 0755)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("main.js")
	w.Write([]byte("console.log(1)"))
	w, _ = zw.Create("lib/util.js")
	w.Write([]byte("export {}"))
	zw.Close()

	if err := extractArchive(buf.Bytes(), dest); err != nil {
		t.Fatalf("正常 zip 应解压成功: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "main.js")); err != nil || string(b) != "console.log(1)" {
		t.Fatalf("main.js 内容不对: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "lib", "util.js")); err != nil {
		t.Fatalf("子目录条目未写出: %v", err)
	}
}

// 判据：拒绝的**原因**要说对。
//
// 为什么单独钉：前缀校验能兜住所有越界形态，但它给出的报错是笼统的
// "越出目标目录"。若"含 .. 段"和"绝对路径"两条显式检查被删掉，
// 越界仍会被挡住（判据①②照样绿），但日志不再说明真实原因 ——
// 排查的人会以为是自己目录配错了。
func TestSafeArchiveTargetReasons(t *testing.T) {
	root := "/tmp/root"
	cases := []struct {
		entry string
		want  string
	}{
		{"../../x", ".."},
		{"a/../../x", ".."},
		{"/etc/passwd", "绝对路径"},
		{`\windows\x`, "绝对路径"},
		{"", "为空"},
	}
	for _, c := range cases {
		_, err := safeArchiveTarget(root, c.entry)
		if err == nil {
			t.Errorf("safeArchiveTarget(%q) 应报错", c.entry)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("safeArchiveTarget(%q) 的报错应含 %q，实际: %v", c.entry, c.want, err)
		}
	}
}

// ── 名字校验本身 ────────────────────────────────────────────

// 判据：checkSafeName 的拒绝清单。
//
// 这一条是"快"判据：上面的行为判据要造目录树，这条直接钉规则表。
// 两条都要有 —— 这条能在调用点被误删时仍然通过，
// 所以它**不能**替代上面的行为判据，只作为规则表的文档。
func TestCheckSafeNameRules(t *testing.T) {
	bad := []string{"", " ", ".", "..", "../x", "a/b", "/etc", `a\b`, ".hidden"}
	for _, n := range bad {
		if err := checkSafeName(n); err == nil {
			t.Errorf("checkSafeName(%q) 应拒绝", n)
		}
	}
	good := []string{"qq", "bilibili-all-in-one", "clawhub_pkg", "x2"}
	for _, n := range good {
		if err := checkSafeName(n); err != nil {
			t.Errorf("checkSafeName(%q) 应放行: %v", n, err)
		}
	}
}

// 判据：resolveUnderRoot 的结果必须真的在根下，且根自身被拒。
func TestResolveUnderRootStaysInside(t *testing.T) {
	root := t.TempDir()
	got, err := resolveUnderRoot(root, "pkg")
	if err != nil {
		t.Fatalf("正常名字应放行: %v", err)
	}
	if !strings.HasPrefix(got, filepath.Clean(root)+string(filepath.Separator)) {
		t.Fatalf("结果不在根下: %s", got)
	}
	// 名字校验漏了某种形态时，前缀校验必须兜住。
	// （这里直接调 ensureWithin，模拟"名字校验失效"的下游防线。）
	if err := ensureWithin(root, filepath.Join(root, "..", "outside")); err == nil {
		t.Fatal("ensureWithin 必须拒绝根外路径")
	}
	if err := ensureWithin(root, root); err != nil {
		t.Fatalf("根自身应放行（归档里的 ./ 之类）: %v", err)
	}
}
