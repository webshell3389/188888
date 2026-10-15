package store

// 回填：启动时扫描 capture/ 下结构化端点（u/war/nb/a/result），
// 把既有数据包完整解析入库。幂等（依赖各表 UNIQUE + INSERT IGNORE/ODKU），
// 可在服务启动后台异步执行，不影响主链可用。

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// backfillFolders 需要完整解析落库的回传端点。
// beacon 也在内：beacon 携带 device_info，回填可为"只有 beacon"的历史设备补出档案。
var backfillFolders = []string{"u", "war", "nb", "a", "beacon", "result"}

// BackfillFull 扫描 capture/<ep>/ 下 .json，对每个文件找到（或补建）packets 指针
// 后执行 ProcessFull。返回各端点解析文件数。任一步失败静默跳过。
func BackfillFull(captureRoot string) map[string]any {
	out := map[string]any{}
	db := GetDB()
	if db == nil {
		return out
	}
	for _, ep := range backfillFolders {
		dir := filepath.Join(captureRoot, ep)
		entries, err := os.ReadDir(dir)
		if err != nil {
			out[ep] = 0
			continue
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		count := 0
		for _, name := range names {
			rel := ep + "/" + name
			pid := packetIDForPath(db, rel)
			raw, rerr := os.ReadFile(filepath.Join(dir, name))
			if rerr != nil {
				continue
			}
			if pid == 0 {
			// 未索引的存量文件：先补 packets 指针，再解析（同指纹历史重复的 dup 落 TRUE 但内容一致，幂等）。
			// 历史文件来源 IP 不可考，srcIP 传空（列表回退 nearestIPAt 推断）。
			pid, _ = RecordPacket(ep, rel, len(raw), raw, "")
			}
			if pid == 0 {
				continue
			}
			ProcessFull(ep, raw, pid)
			count++
		}
		out[ep] = count
	}
	return out
}

func packetIDForPath(db *DB, rel string) int64 {
	row := db.QueryOne("SELECT id FROM packets WHERE file_path=?", rel)
	if row == nil {
		return 0
	}
	return toInt(row["id"])
}
