# 单容器部署：前端资源嵌入二进制，不依赖 nginx。
#
# 语义检索需要 CGO（onnxruntime + OpenVINO + libjpeg-turbo），alpine + CGO_ENABLED=0
# 无法承载，因此基底换成 Debian bookworm，构建参数与 Dockerfile.server 保持一致。
#
# 模型不进镜像（两份 ONNX 合计 718MB），运行时挂到 /data/models/<model_id>，
# 正好是默认解析路径，零配置生效：
#   docker run -v $PWD/data:/data -v $PWD/server/models:/data/models:ro \
#              --device /dev/dri:/dev/dri --group-add render ...
#
# 两处网络适配（目标机实测；换网络环境可用 --build-arg 覆盖）：
# - 容器内 apt 会先试 IPv6，无 IPv6 路由时每个请求都要等连接超时。实测 apt update
#   从 98 秒降到数秒，所以所有 apt-get 都带 -o Acquire::ForceIPv4=true。
# - deb.debian.org 在国内慢，默认换 aliyun 镜像；要走官方源传
#   --build-arg APT_MIRROR=deb.debian.org。

# ---- 公共前置：Debian 系阶段共用的换源 ----
FROM debian:bookworm-slim AS debian-base
ARG APT_MIRROR=mirrors.aliyun.com
RUN set -eux; \
    for f in /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources; do \
      if [ -f "$f" ]; then \
        sed -i "s|deb.debian.org|${APT_MIRROR}|g; s|security.debian.org|${APT_MIRROR}|g" "$f"; \
      fi; \
    done

# ---- 推理运行库抽取 ----
# 基底用 debian 而不是 python 镜像：这里只需要 wheel 里的 .so，wheel 本质是 zip，
# 借 pip 只为省掉手写索引解析。固定 cp312 tag，与构建机解释器无关
# （onnxruntime 的 C 库不分 cp 版本）。
FROM debian-base AS ortlibs
ARG ORT_VERSION=1.24.1
ARG PIP_INDEX=https://pypi.tuna.tsinghua.edu.cn/simple
RUN apt-get -o Acquire::ForceIPv4=true update \
 && apt-get -o Acquire::ForceIPv4=true install -y --no-install-recommends \
      python3-pip ca-certificates \
 && rm -rf /var/lib/apt/lists/*
# 只装 onnxruntime-openvino：与 onnxruntime 混装会把不同版本的
# libonnxruntime.so 放进同一目录，运行时加载哪个取决于 LD_LIBRARY_PATH 顺序。
RUN pip3 install --no-cache-dir --no-deps --only-binary=:all: \
      --python-version 312 --break-system-packages --target /ortpkg \
      -i ${PIP_INDEX} onnxruntime-openvino==${ORT_VERSION} \
 && mkdir -p /opt/ort \
 && cp -a /ortpkg/onnxruntime/capi/*.so* /opt/ort/ \
 && ls -la /opt/ort

# ---- 前端 ----
FROM node:20-alpine AS web-builder
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

# ---- 后端 ----
FROM golang:1.22-bookworm AS go-builder
WORKDIR /build
ENV GOPROXY=https://goproxy.cn,direct
ARG APT_MIRROR=mirrors.aliyun.com
# libjpeg62-turbo-dev 提供 jpeglib.h，供 internal/imgcodec 的 DCT 降采样路径使用。
RUN set -eux; \
    for f in /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources; do \
      if [ -f "$f" ]; then \
        sed -i "s|deb.debian.org|${APT_MIRROR}|g; s|security.debian.org|${APT_MIRROR}|g" "$f"; \
      fi; \
    done; \
    apt-get -o Acquire::ForceIPv4=true update; \
    apt-get -o Acquire::ForceIPv4=true install -y --no-install-recommends libjpeg62-turbo-dev; \
    rm -rf /var/lib/apt/lists/*
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ .
COPY --from=web-builder /web/dist ./cmd/server/web
RUN CGO_ENABLED=1 go build -tags "withweb libjpeg" -ldflags="-s -w" -o mini-nas-server ./cmd/server

# ---- 运行 ----
FROM debian-base
# 原镜像装的 ffmpeg 在服务端代码里没有任何调用点，已移除（省 ~200MB）。
RUN apt-get -o Acquire::ForceIPv4=true update \
 && apt-get -o Acquire::ForceIPv4=true install -y --no-install-recommends \
      ca-certificates tzdata libjpeg62-turbo \
 && rm -rf /var/lib/apt/lists/*

# iGPU 用户态驱动。装不上就退化为 CPU 推理（索引慢约 6 倍），
# 但不让整个镜像构建失败——/api/index/health 会暴露 device，运维能立刻看到降级。
RUN apt-get -o Acquire::ForceIPv4=true update \
 && ( apt-get -o Acquire::ForceIPv4=true install -y --no-install-recommends \
        intel-opencl-icd libze-intel-gpu1 \
      || apt-get -o Acquire::ForceIPv4=true install -y --no-install-recommends \
        intel-opencl-icd intel-level-zero-gpu libze1 \
      || echo "WARN: iGPU 用户态驱动未安装，语义索引将使用 CPU" ) \
 && rm -rf /var/lib/apt/lists/*

COPY --from=ortlibs /opt/ort /opt/ort
ENV LD_LIBRARY_PATH=/opt/ort

RUN useradd --system --create-home --home-dir /data --shell /usr/sbin/nologin mini-nas

WORKDIR /app
COPY --from=go-builder /build/mini-nas-server .
RUN mkdir -p /data && chown -R mini-nas:mini-nas /data /app
USER mini-nas
EXPOSE 8080
VOLUME ["/data"]
CMD ["./mini-nas-server", "-config", "/data/config.json", "-data", "/data"]
