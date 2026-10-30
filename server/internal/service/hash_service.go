package service

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/zhangjh/mini-nas/server/internal/model"
	"github.com/zhangjh/mini-nas/server/internal/store"
)

// 哈希缓冲区大小。NAS 场景多为顺序大文件读，256KB 能在吞吐与内存间取得平衡。
const hashBufferSize = 256 * 1024

// HashService 负责内容指纹的计算、持久化与去重判定。
//
// 路径一律使用虚拟路径（mount/rel），与 API 层保持一致；宿主机绝对路径
// 通过注入的 absPath 解析，避免把 HOST_ROOT 映射关系写进数据库。
type HashService struct {
	db      *store.DB
	absPath func(virtualPath string) string
}

func NewHashService(db *store.DB, absPath func(string) string) *HashService {
	return &HashService{db: db, absPath: absPath}
}

// MD5File 计算文件的完整 MD5。
func MD5File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return md5Reader(f, -1)
}

// PartialMD5 只计算文件前 n 字节的 MD5，用于大文件去重预筛。
//
// 大文件全量哈希代价高，先用「大小 + 前 N 字节」缩小候选，再对候选做
// 全量哈希，可以把 IO 量降到接近真实重复数据的规模。
func PartialMD5(path string, n int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return md5Reader(f, n)
}

// md5Reader 计算 r 的 MD5；limit <= 0 表示读到 EOF。
func md5Reader(r io.Reader, limit int64) (string, error) {
	h := md5.New()
	buf := make([]byte, hashBufferSize)

	var (
		total int64
	)
	for {
		readSize := len(buf)
		if limit > 0 {
			remaining := limit - total
			if remaining <= 0 {
				break
			}
			if int64(readSize) > remaining {
				readSize = int(remaining)
			}
		}

		n, err := r.Read(buf[:readSize])
		if n > 0 {
			h.Write(buf[:n])
			total += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// MD5Bytes 计算字节切片的 MD5。
func MD5Bytes(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

// Record 写入某个虚拟路径的内容指纹。
func (s *HashService) Record(virtualPath, md5 string, size, mtime int64) error {
	return s.db.UpsertFileHash(model.FileHash{
		Path:       virtualPath,
		Size:       size,
		MTime:      mtime,
		MD5:        md5,
		ComputedAt: time.Now().UnixMilli(),
	})
}

// FindDuplicate 判断给定内容是否已存在，返回既有文件的虚拟路径。
//
// 仅凭数据库记录不足以下结论：文件可能已被外部删除，因此这里会校验
// 目标文件仍然存在且大小一致，避免把上传引向一个不存在的「原件」。
func (s *HashService) FindDuplicate(md5sum string, size int64, excludePath string) (string, bool) {
	path, err := s.db.FindByMD5(md5sum, size, excludePath)
	if err != nil {
		return "", false
	}

	abs := s.absPath(path)
	if abs == "" {
		return "", false
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() || info.Size() != size {
		// 记录已过期，顺手清理，避免后续反复命中脏数据。
		_ = s.db.DeleteFileHash(path)
		return "", false
	}
	return path, true
}

// HashOf 返回某个虚拟路径的内容指纹，缓存未命中时现算并落库。
func (s *HashService) HashOf(virtualPath string) (*model.FileHash, error) {
	if h, err := s.db.GetFileHash(virtualPath); err == nil {
		return h, nil
	}

	abs := s.absPath(virtualPath)
	if abs == "" {
		return nil, fmt.Errorf("path not resolved: %s", virtualPath)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}

	sum, err := MD5File(abs)
	if err != nil {
		return nil, err
	}
	h := model.FileHash{
		Path:       virtualPath,
		Size:       info.Size(),
		MTime:      info.ModTime().UnixMilli(),
		MD5:        sum,
		ComputedAt: time.Now().UnixMilli(),
	}
	if err := s.db.UpsertFileHash(h); err != nil {
		return nil, err
	}
	return &h, nil
}

// Forget 在文件被删除或改名后清除其指纹。
func (s *HashService) Forget(virtualPath string) error {
	return s.db.DeleteFileHash(virtualPath)
}

// ForgetUnder 清除某个虚拟目录下所有指纹。
func (s *HashService) ForgetUnder(prefix string) error {
	return s.db.DeleteFileHashUnder(prefix)
}

// ---- 去重跳过记录 ----

// RecordSkip 记录一次跳过写入。
func (s *HashService) RecordSkip(virtualPath, md5sum string, size, mtime int64, duplicateOf string) error {
	return s.db.RecordSkip(model.DedupSkip{
		Path:        virtualPath,
		MD5:         md5sum,
		Size:        size,
		MTime:       mtime,
		DuplicateOf: duplicateOf,
		CreatedAt:   time.Now().UnixMilli(),
	})
}

// ForgetSkip 删除跳过记录。
func (s *HashService) ForgetSkip(virtualPath string) error {
	return s.db.DeleteSkip(virtualPath)
}

// Skip 读取单条跳过记录。
func (s *HashService) Skip(virtualPath string) (*model.DedupSkip, error) {
	return s.db.GetSkip(virtualPath)
}

// ListSkips 列出某虚拟目录下的跳过记录。
func (s *HashService) ListSkips(prefix string) ([]model.DedupSkip, error) {
	return s.db.ListSkipsUnder(prefix)
}

// SkipStats 汇总跳过记录。
func (s *HashService) SkipStats() (store.SkipStats, error) {
	return s.db.GetSkipStats()
}

// ListSkipsVerified 列出跳过记录，并剔除原件已消失的悬空条目。
//
// 裸的 ListSkips 会把悬空记录一并返回：原件被删除后，那条「已去重未落盘」
// 的登记就没有任何意义了，对外展示会让用户以为文件还在。
// ListTree 自己会做这层校验，但 API 与统计如果直接用裸查询就会虚高。
func (s *HashService) ListSkipsVerified(prefix string) ([]model.DedupSkip, error) {
	skips, err := s.db.ListSkipsUnder(prefix)
	if err != nil {
		return nil, err
	}
	out := skips[:0]
	for i := range skips {
		if s.VerifySkipTarget(&skips[i]) {
			out = append(out, skips[i])
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// SkipStatsVerified 只统计原件仍然存在的跳过记录。
//
// 「节省空间」这个数字只有在原件还在时才成立——原件删了，当初省下的字节
// 就已经真的省下来了，不该再算进待复用统计里。
func (s *HashService) SkipStatsVerified() (store.SkipStats, error) {
	skips, err := s.db.ListSkipsUnder("")
	if err != nil {
		return store.SkipStats{}, err
	}
	var st store.SkipStats
	for i := range skips {
		if !s.VerifySkipTarget(&skips[i]) {
			continue
		}
		st.Count++
		st.Bytes += skips[i].Size
	}
	return st, nil
}

// VerifySkipTarget 校验跳过记录指向的原件是否仍然存在。
//
// 原件被删除后，跳过记录就成了悬空引用——此时该路径既没有真实文件，
// 也无法再复用原件，必须让上层知道需要重新上传。
func (s *HashService) VerifySkipTarget(skip *model.DedupSkip) bool {
	abs := s.absPath(skip.DuplicateOf)
	if abs == "" {
		return false
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() || info.Size() != skip.Size {
		return false
	}
	return true
}

// AbsPathOf 暴露路径解析，供扫描等需要直接读文件的地方使用。
func (s *HashService) AbsPathOf(virtualPath string) string {
	return s.absPath(virtualPath)
}
