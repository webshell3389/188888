package forensics

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"mockc2/internal/config"
)

// tools/ 取证工具挂载：调用 Python 工具解析回传的 /war（keychain）取证 JSON。
//
// 环境变量：
//   MOCKC2_FORENSICS_PY    python 命令（可含参数，空格分隔）。默认 windows=python / 其它=python3。
//   MOCKC2_FORENSICS_TOOLS 工具目录。默认 <root>/tools。
//
// 产物：capture/war_report/<name>_smart.json（smart_forensics 的机器可读报告）。

func pyPrefix() []string {
	if v := strings.TrimSpace(os.Getenv("MOCKC2_FORENSICS_PY")); v != "" {
		return strings.Fields(v)
	}
	if runtime.GOOS == "windows" {
		return []string{"python"}
	}
	return []string{"python3"}
}

// ToolsDir 工具目录。
func ToolsDir() string {
	if v := strings.TrimSpace(os.Getenv("MOCKC2_FORENSICS_TOOLS")); v != "" {
		return v
	}
	return filepath.Join(config.Get().RootDir, "tools")
}

// ReportDir 报告输出目录。
func ReportDir() string {
	return filepath.Join(config.Get().CaptureRoot, "war_report")
}

// reportPathFor 某 war 文件对应的报告缓存路径。
func reportPathFor(warBase string) string {
	base := strings.TrimSuffix(warBase, filepath.Ext(warBase))
	return filepath.Join(ReportDir(), base+"_smart.json")
}

func tailStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// RunSmart 对 war 文件运行 smart_forensics.py，返回其 _smart.json。
// refresh=false 且已有缓存时直接返回缓存。
func RunSmart(warAbs string, refresh bool) (map[string]any, error) {
	base := filepath.Base(warAbs)
	rep := reportPathFor(base)
	if !refresh {
		if b, err := os.ReadFile(rep); err == nil {
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				m["cached"] = true
				return m, nil
			}
		}
	}
	if err := os.MkdirAll(ReportDir(), 0o755); err != nil {
		return nil, err
	}
	prefix := pyPrefix()
	args := append([]string{}, prefix[1:]...)
	args = append(args, filepath.Join(ToolsDir(), "smart_forensics.py"), warAbs, "--out", ReportDir())
	cmd := exec.Command(prefix[0], args...)
	cmd.Dir = ToolsDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]any{"error": err.Error(), "output": tailStr(string(out), 4000)}, err
	}
	b, rerr := os.ReadFile(rep)
	if rerr != nil {
		return map[string]any{"output": tailStr(string(out), 4000), "error": "报告未生成"}, rerr
	}
	var m map[string]any
	if e := json.Unmarshal(b, &m); e != nil {
		return nil, e
	}
	m["cached"] = false
	return m, nil
}

// RecoverAll 运行 recover_all.py（9 类钱包助记词恢复，较慢）。
// pw 为显式候选口令；deepPin 追加全量四位 PIN。返回其输出的 *_allin1.json。
func RecoverAll(warAbs string, pw []string, deepPin, refresh bool) (map[string]any, error) {
	base := strings.TrimSuffix(filepath.Base(warAbs), filepath.Ext(warAbs))
	rep := filepath.Join(ReportDir(), base+"_allin1.json")
	if !refresh {
		if b, err := os.ReadFile(rep); err == nil {
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				m["cached"] = true
				return m, nil
			}
		}
	}
	if err := os.MkdirAll(ReportDir(), 0o755); err != nil {
		return nil, err
	}
	prefix := pyPrefix()
	args := append([]string{}, prefix[1:]...)
	args = append(args, filepath.Join(ToolsDir(), "recover_all.py"), warAbs, "--out", ReportDir())
	if len(pw) > 0 {
		args = append(args, "--pw")
		args = append(args, pw...)
	}
	if deepPin {
		args = append(args, "--deep-pin")
	}
	cmd := exec.Command(prefix[0], args...)
	cmd.Dir = ToolsDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]any{"error": err.Error(), "output": tailStr(string(out), 4000)}, err
	}
	b, rerr := os.ReadFile(rep)
	if rerr != nil {
		return map[string]any{"output": tailStr(string(out), 4000), "error": "报告未生成"}, rerr
	}
	var m map[string]any
	if e := json.Unmarshal(b, &m); e != nil {
		return nil, e
	}
	m["cached"] = false
	return m, nil
}
