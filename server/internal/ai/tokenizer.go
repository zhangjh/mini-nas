package ai

import (
	"fmt"
	"os"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// 特殊 token 的字面量。id 不硬编码，从 vocab.txt 里查——虽然 CN-CLIP 的
// 取值固定是 PAD=0/UNK=100/CLS=101/SEP=102/MASK=103，但换模型后不该假设。
const (
	tokPad  = "[PAD]"
	tokUnk  = "[UNK]"
	tokCls  = "[CLS]"
	tokSep  = "[SEP]"
	tokMask = "[MASK]"

	// maxCharsPerWord 是 WordPiece 的单词长度上限，超出直接判 [UNK]，
	// 与 HuggingFace 的实现保持一致。
	maxCharsPerWord = 100
)

// Tokenizer 是 BERT WordPiece 分词器的 Go 实现。
//
// 为什么不用现成库：导出脚本只产出 vocab.txt（一行一个 token），没有
// tokenizer.json，通用库要么要求完整配置、要么行为与 BertTokenizer
// 有细微差别（中文按字切分、标点切分、## 续接）。这里手写约 200 行，
// 并用 golden.npz 里的 input_ids 做逐 token 对拍。
type Tokenizer struct {
	vocab  map[string]int32
	MaxLen int

	UnkID int32
	ClsID int32
	SepID int32
	PadID int32
}

// LoadTokenizer 读取 vocab.txt（行号即 token id）。
func LoadTokenizer(path string, maxLen int) (*Tokenizer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 vocab.txt 失败: %w", err)
	}
	if maxLen <= 0 {
		return nil, fmt.Errorf("maxLen 必须为正数")
	}

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	vocab := make(map[string]int32, len(lines))
	for i, line := range lines {
		tok := strings.TrimRight(line, "\r")
		if tok == "" && i == len(lines)-1 {
			continue // 文件末尾的空行
		}
		if _, exists := vocab[tok]; exists {
			continue // 词表里理论上没有重复，重复时保留首次出现的 id
		}
		vocab[tok] = int32(i)
	}

	t := &Tokenizer{vocab: vocab, MaxLen: maxLen}
	var ok bool
	if t.PadID, ok = vocab[tokPad]; !ok {
		return nil, fmt.Errorf("vocab.txt 缺少 %s", tokPad)
	}
	if t.UnkID, ok = vocab[tokUnk]; !ok {
		return nil, fmt.Errorf("vocab.txt 缺少 %s", tokUnk)
	}
	if t.ClsID, ok = vocab[tokCls]; !ok {
		return nil, fmt.Errorf("vocab.txt 缺少 %s", tokCls)
	}
	if t.SepID, ok = vocab[tokSep]; !ok {
		return nil, fmt.Errorf("vocab.txt 缺少 %s", tokSep)
	}
	return t, nil
}

// Encode 把文本编码成固定长度（MaxLen）的 input_ids 与 attention_mask。
//
// 固定长度是静态 shape 的要求（模型输入 1×64），不是可以「顺手放宽」的参数。
func (t *Tokenizer) Encode(text string) (ids []int64, mask []int64) {
	pieces := t.tokenize(text)

	// 留出 [CLS] 与 [SEP] 两个位置。
	maxPieces := t.MaxLen - 2
	if maxPieces < 0 {
		maxPieces = 0
	}
	if len(pieces) > maxPieces {
		pieces = pieces[:maxPieces]
	}

	ids = make([]int64, t.MaxLen)
	mask = make([]int64, t.MaxLen)
	for i := range ids {
		ids[i] = int64(t.PadID)
	}

	ids[0] = int64(t.ClsID)
	mask[0] = 1
	for i, p := range pieces {
		id, ok := t.vocab[p]
		if !ok {
			id = t.UnkID
		}
		ids[i+1] = int64(id)
		mask[i+1] = 1
	}
	sep := 1 + len(pieces)
	if sep < t.MaxLen {
		ids[sep] = int64(t.SepID)
		mask[sep] = 1
	}
	return ids, mask
}

// Tokenize 返回可读的 token 序列（含 [CLS]/[SEP]），用于排查分词问题。
func (t *Tokenizer) Tokenize(text string) []string {
	out := []string{tokCls}
	out = append(out, t.tokenize(text)...)
	return append(out, tokSep)
}

func (t *Tokenizer) tokenize(text string) []string {
	cleaned := tokenizeChineseChars(cleanText(text))
	var out []string
	for _, tok := range strings.Fields(cleaned) {
		tok = strings.ToLower(tok)
		tok = stripAccents(tok)
		for _, piece := range splitOnPunc(tok) {
			out = append(out, t.wordPiece(piece)...)
		}
	}
	return out
}

// wordPiece 做贪心最长匹配，续接片段加 "##" 前缀。
func (t *Tokenizer) wordPiece(token string) []string {
	runes := []rune(token)
	if len(runes) == 0 {
		return nil
	}
	if len(runes) > maxCharsPerWord {
		return []string{tokUnk}
	}

	out := make([]string, 0, len(runes))
	start := 0
	for start < len(runes) {
		end := len(runes)
		var cur string
		found := false
		for start < end {
			sub := string(runes[start:end])
			if start > 0 {
				sub = "##" + sub
			}
			if _, ok := t.vocab[sub]; ok {
				cur = sub
				found = true
				break
			}
			end--
		}
		if !found {
			return []string{tokUnk}
		}
		out = append(out, cur)
		start = end
	}
	return out
}

// cleanText 去掉控制字符，把各类空白统一成空格。
func cleanText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == 0 || r == 0xFFFD:
			continue
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(' ')
		case unicode.IsControl(r):
			continue
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// tokenizeChineseChars 给每个汉字两侧补空格，让后续按空白切分时汉字单独成 token。
//
// 中文没有空格，不这么做「一只猫」会被当成一个词去查词表，直接落进 [UNK]。
func tokenizeChineseChars(s string) string {
	var b strings.Builder
	b.Grow(len(s) * 2)
	for _, r := range s {
		if isChinese(r) {
			b.WriteRune(' ')
			b.WriteRune(r)
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isChinese 判定汉字，覆盖 CJK 统一表意文字及其扩展区、兼容区。
func isChinese(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x20000 && r <= 0x2A6DF) ||
		(r >= 0x2A700 && r <= 0x2B73F) ||
		(r >= 0x2B740 && r <= 0x2B81F) ||
		(r >= 0x2B820 && r <= 0x2CEAF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0x2F800 && r <= 0x2FA1F)
}

// splitOnPunc 在标点处切开，标点自身单独成 token。
func splitOnPunc(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		if isPunctuation(r) {
			flush()
			out = append(out, string(r))
			continue
		}
		cur.WriteRune(r)
	}
	flush()
	return out
}

// isPunctuation 与 HuggingFace BasicTokenizer._is_punctuation 对齐：
// ASCII 可见标点全集 + Unicode P* 类别（不含 S* 符号）。
func isPunctuation(r rune) bool {
	if (r >= 33 && r <= 47) || (r >= 58 && r <= 64) || (r >= 91 && r <= 96) || (r >= 123 && r <= 126) {
		return true
	}
	return unicode.IsPunct(r)
}

// stripAccents 去掉变音符号（NFD 分解后丢弃 Mn 类别）。
func stripAccents(s string) string {
	decomposed := norm.NFD.String(s)
	var b strings.Builder
	b.Grow(len(decomposed))
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
