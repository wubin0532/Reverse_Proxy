package upgrade

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// payloadMarker 自解压脚本中标记 payload 起始的行。
const payloadMarker = "__PAYLOAD_BELOW__"

// binaryName tar 包内的二进制文件名。
const binaryName = "andey-proxy"

// elfMagic ELF 文件头魔数，用于校验解出的确实是可执行文件。
var elfMagic = []byte{0x7f, 'E', 'L', 'F'}

// extractPayload 解析 .run 自解压包：脚本头之后、payloadMarker 下一行开始为 tar.gz，
// 解出其中名为 andey-proxy 的二进制写入临时目录，返回临时目录路径（二进制位于其下）。
func extractPayload(runPath string) (string, error) {
	f, err := os.Open(runPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// 逐行扫描脚本头，定位 payload 起点
	br := bufio.NewReader(f)
	for {
		line, err := br.ReadString('\n')
		if trimLine(line) == payloadMarker {
			break
		}
		if err != nil {
			return "", errors.New("安装包格式错误：未找到 payload 标记")
		}
	}

	gz, err := gzip.NewReader(br)
	if err != nil {
		return "", fmt.Errorf("安装包格式错误：payload 不是有效的 gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", errors.New("安装包格式错误：包内未找到 " + binaryName)
		}
		if err != nil {
			return "", fmt.Errorf("读取安装包失败: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != binaryName {
			continue
		}
		return writeBinary(tr)
	}
}

// writeBinary 将 tar 条目内容写入临时目录，写前校验 ELF 魔数。
func writeBinary(r io.Reader) (string, error) {
	head := make([]byte, len(elfMagic))
	if _, err := io.ReadFull(r, head); err != nil {
		return "", fmt.Errorf("读取二进制失败: %w", err)
	}
	if string(head) != string(elfMagic) {
		return "", errors.New("安装包内容校验失败：解出的文件不是 ELF 可执行文件")
	}

	dir, err := os.MkdirTemp("", "andey-proxy-upgrade-*")
	if err != nil {
		return "", err
	}
	out, err := os.OpenFile(filepath.Join(dir, binaryName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	if _, err := out.Write(head); err != nil {
		out.Close()
		os.RemoveAll(dir)
		return "", err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		os.RemoveAll(dir)
		return "", fmt.Errorf("写出二进制失败: %w", err)
	}
	if err := out.Close(); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func trimLine(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
