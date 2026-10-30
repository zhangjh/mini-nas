package ai

import (
	"archive/zip"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

// NPZ 是 .npz 归档（多个 .npy 的 zip）的解析结果。
//
// 只服务于自检夹具（golden.npz / preproc_golden.npz），因此刻意做窄：
// 支持小端 f4/i8/i4/u1、C 顺序，不处理 fortran_order 与大端。
// 为这点需求引入 gonum 之类的依赖不划算，而夹具本身是固定的。
type NPZ map[string]*NpyArray

// NpyArray 是一个 .npy 数组：形状、dtype 与原始字节。
type NpyArray struct {
	Shape []int
	DType string
	Data  []byte
}

// Len 返回元素个数。
func (a *NpyArray) Len() int {
	n := 1
	for _, d := range a.Shape {
		n *= d
	}
	return n
}

// LoadNPZ 读取一个 .npz 文件。
func LoadNPZ(path string) (NPZ, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("打开 npz %s: %w", path, err)
	}
	defer zr.Close()

	out := make(NPZ, len(zr.File))
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".npy") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("读取 npz 成员 %s: %w", f.Name, err)
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("读取 npz 成员 %s: %w", f.Name, err)
		}
		arr, err := parseNpy(raw)
		if err != nil {
			return nil, fmt.Errorf("解析 %s: %w", f.Name, err)
		}
		out[strings.TrimSuffix(f.Name, ".npy")] = arr
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("npz %s 中没有 .npy 成员", path)
	}
	return out, nil
}

// MustLoadNPZ 用于自检路径：文件缺失属于部署错误，直接返回错误由调用方记录。
func MustLoadNPZ(path string) (NPZ, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("夹具不存在: %s", path)
	}
	return LoadNPZ(path)
}

const npyMagic = "\x93NUMPY"

func parseNpy(raw []byte) (*NpyArray, error) {
	if len(raw) < 12 || string(raw[:6]) != npyMagic {
		return nil, fmt.Errorf("不是合法的 npy 数据")
	}
	major := raw[6]

	var headerLen, dataOff int
	switch major {
	case 1:
		headerLen = int(binary.LittleEndian.Uint16(raw[8:10]))
		dataOff = 10 + headerLen
	case 2, 3:
		headerLen = int(binary.LittleEndian.Uint32(raw[8:12]))
		dataOff = 12 + headerLen
	default:
		return nil, fmt.Errorf("不支持的 npy 版本 %d", major)
	}
	if dataOff > len(raw) {
		return nil, fmt.Errorf("npy 头部长度越界")
	}
	header := string(raw[dataOff-headerLen : dataOff])

	arr := &NpyArray{}
	descr, err := npzQuoted(header, "descr")
	if err != nil {
		return nil, err
	}
	arr.DType = descr

	shape, err := npzShape(header)
	if err != nil {
		return nil, err
	}
	arr.Shape = shape

	if strings.Contains(header, "'fortran_order': True") {
		return nil, fmt.Errorf("不支持 fortran_order=True")
	}

	want := arr.Len() * dtypeSize(descr)
	if want == 0 {
		return nil, fmt.Errorf("不支持的 dtype %q", descr)
	}
	if len(raw)-dataOff < want {
		return nil, fmt.Errorf("npy 数据长度不足：期望 %d，实际 %d", want, len(raw)-dataOff)
	}
	arr.Data = raw[dataOff : dataOff+want]
	return arr, nil
}

func dtypeSize(dtype string) int {
	switch dtype {
	case "<f4", "<i4":
		return 4
	case "<i8", "<f8":
		return 8
	case "|u1", "|i1", "<u1", "<i1":
		return 1
	}
	return 0
}

// npzQuoted 取出 header 里某个键的字符串值，例如 'descr': '<f4'。
func npzQuoted(header, key string) (string, error) {
	marker := "'" + key + "':"
	i := strings.Index(header, marker)
	if i < 0 {
		return "", fmt.Errorf("npy 头部缺少 %s", key)
	}
	rest := header[i+len(marker):]
	start := strings.IndexByte(rest, '\'')
	if start < 0 {
		return "", fmt.Errorf("npy 头部 %s 格式异常", key)
	}
	rest = rest[start+1:]
	end := strings.IndexByte(rest, '\'')
	if end < 0 {
		return "", fmt.Errorf("npy 头部 %s 格式异常", key)
	}
	return rest[:end], nil
}

// npzShape 解析 'shape': (1, 3, 224, 224)。
func npzShape(header string) ([]int, error) {
	marker := "'shape':"
	i := strings.Index(header, marker)
	if i < 0 {
		return nil, fmt.Errorf("npy 头部缺少 shape")
	}
	rest := header[i+len(marker):]
	open := strings.IndexByte(rest, '(')
	closeIdx := strings.IndexByte(rest, ')')
	if open < 0 || closeIdx < 0 || closeIdx < open {
		return nil, fmt.Errorf("npy 头部 shape 格式异常")
	}
	body := strings.TrimSpace(rest[open+1 : closeIdx])
	if body == "" {
		return nil, nil
	}
	parts := strings.Split(body, ",")
	shape := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("npy shape 项 %q 非法", p)
		}
		shape = append(shape, n)
	}
	return shape, nil
}

// Float32 返回 float32 视图（小端）。
func (a *NpyArray) Float32() ([]float32, error) {
	if a.DType != "<f4" {
		return nil, fmt.Errorf("期望 <f4，实际 %s", a.DType)
	}
	out := make([]float32, a.Len())
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(a.Data[i*4:]))
	}
	return out, nil
}

// Int64 返回 int64 视图（小端）。
func (a *NpyArray) Int64() ([]int64, error) {
	if a.DType != "<i8" {
		return nil, fmt.Errorf("期望 <i8，实际 %s", a.DType)
	}
	out := make([]int64, a.Len())
	for i := range out {
		out[i] = int64(binary.LittleEndian.Uint64(a.Data[i*8:]))
	}
	return out, nil
}

// Uint8 返回原始字节（uint8 数组或 PNG 字节流）。
func (a *NpyArray) Uint8() ([]byte, error) {
	switch a.DType {
	case "|u1", "<u1", "|i1", "<i1":
		out := make([]byte, len(a.Data))
		copy(out, a.Data)
		return out, nil
	}
	return nil, fmt.Errorf("期望 u1，实际 %s", a.DType)
}
