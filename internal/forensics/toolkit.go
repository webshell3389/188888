package forensics

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
)

// WriteToolkitTarGz 把 tools/ 目录（脚本 + 词表 + docs）打包为 tar.gz 写入 w。
// 供前端「下载工具包」——自动检测不全时，手动下载到本地用完整工具分析。
func WriteToolkitTarGz(w io.Writer) error {
	root := ToolsDir()
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		hdr, herr := tar.FileInfoHeader(info, "")
		if herr != nil {
			return nil
		}
		hdr.Name = filepath.ToSlash(filepath.Join("forensics_toolkit", "tools", rel))
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, oerr := os.Open(p)
		if oerr != nil {
			return nil
		}
		defer f.Close()
		_, _ = io.Copy(tw, f)
		return nil
	})
}
