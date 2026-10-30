//go:build cgo && libjpeg

package imgcodec

/*
#cgo LDFLAGS: -ljpeg
#include <stdio.h>
#include <stdlib.h>
#include <jpeglib.h>
#include <setjmp.h>

typedef struct {
	struct jpeg_error_mgr pub;
	jmp_buf jump;
} mn_jpeg_error;

static void mn_jpeg_error_exit(j_common_ptr cinfo) {
	mn_jpeg_error *err = (mn_jpeg_error *) cinfo->err;
	longjmp(err->jump, 1);
}

static void mn_jpeg_emit_message(j_common_ptr cinfo, int level) {
	(void) cinfo;
	(void) level;
}

// 解码 JPEG，按 DCT 降采样到「长边 >= hint」的最大 1/2^n 尺寸。
// 返回值：0 成功，-1 打不开文件，-2 解码失败。
static int mn_decode_jpeg(const char *path, int hint, int *out_w, int *out_h,
                          unsigned char **out_pix) {
	FILE *fp = fopen(path, "rb");
	if (fp == NULL) {
		return -1;
	}

	struct jpeg_decompress_struct cinfo;
	mn_jpeg_error jerr;
	// buf 在 setjmp 之后才赋值，longjmp 回来时只有 volatile 变量的值才是确定的。
	unsigned char * volatile buf = NULL;
	cinfo.err = jpeg_std_error(&jerr.pub);
	jerr.pub.error_exit = mn_jpeg_error_exit;
	jerr.pub.emit_message = mn_jpeg_emit_message;

	// setjmp/longjmp 都在本函数内，不跨越 cgo 边界。
	if (setjmp(jerr.jump)) {
		if (buf != NULL) {
			free((void *) buf);
		}
		jpeg_destroy_decompress(&cinfo);
		fclose(fp);
		return -2;
	}

	jpeg_create_decompress(&cinfo);
	jpeg_stdio_src(&cinfo, fp);
	jpeg_read_header(&cinfo, TRUE);
	cinfo.out_color_space = JCS_RGB;

	int longest = cinfo.image_width > cinfo.image_height
	              ? (int) cinfo.image_width : (int) cinfo.image_height;
	int denom = 1;
	while (denom < 8 && longest / (denom * 2) >= hint) {
		denom *= 2;
	}
	cinfo.scale_num = 1;
	cinfo.scale_denom = denom;

	jpeg_start_decompress(&cinfo);
	int w = (int) cinfo.output_width;
	int h = (int) cinfo.output_height;
	if (cinfo.output_components != 3 || w <= 0 || h <= 0) {
		jpeg_destroy_decompress(&cinfo);
		fclose(fp);
		return -2;
	}

	buf = (unsigned char *) malloc((size_t) w * (size_t) h * 3);
	if (buf == NULL) {
		jpeg_destroy_decompress(&cinfo);
		fclose(fp);
		return -2;
	}

	int row = w * 3;
	while (cinfo.output_scanline < cinfo.output_height) {
		JSAMPROW rows[1];
		rows[0] = buf + (size_t) cinfo.output_scanline * (size_t) row;
		if (jpeg_read_scanlines(&cinfo, rows, 1) == 0) {
			break;
		}
	}

	jpeg_finish_decompress(&cinfo);
	jpeg_destroy_decompress(&cinfo);
	fclose(fp);

	*out_w = w;
	*out_h = h;
	*out_pix = buf;
	return 0;
}
*/
import "C"

import (
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"unsafe"
)

// decodeFile 优先走 libjpeg-turbo 的 DCT 降采样，失败时退回标准库。
//
// 只有 JPEG 有降采样路径；PNG/GIF/WebP/BMP 仍走标准库——它们没有 DCT
// 这类可以直接在频域降尺寸的结构。
func decodeFile(path string, hint int) (image.Image, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		if img, err := decodeJPEG(path, hint); err == nil {
			return img, nil
		}
		// libjpeg 失败（损坏或异常编码）时交给标准库再试一次，
		// 避免因解码器差异把本来能读的图判死。
	}
	return decodeWithStd(path)
}

func decodeJPEG(path string, hint int) (image.Image, error) {
	if hint <= 0 {
		hint = 224
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var (
		w, h C.int
		pix  *C.uchar
	)
	if rc := C.mn_decode_jpeg(cPath, C.int(hint), &w, &h, &pix); rc != 0 {
		return nil, fmt.Errorf("libjpeg 解码 %s 失败 (%d)", path, int(rc))
	}
	defer C.free(unsafe.Pointer(pix))

	width, height := int(w), int(h)
	buf := C.GoBytes(unsafe.Pointer(pix), C.int(width*height*3))
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		src := y * width * 3
		dst := y * img.Stride
		for x := 0; x < width; x++ {
			img.Pix[dst+x*4] = buf[src+x*3]
			img.Pix[dst+x*4+1] = buf[src+x*3+1]
			img.Pix[dst+x*4+2] = buf[src+x*3+2]
			img.Pix[dst+x*4+3] = 0xff
		}
	}
	return img, nil
}
