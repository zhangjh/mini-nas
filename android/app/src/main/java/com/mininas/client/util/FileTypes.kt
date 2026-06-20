package com.mininas.client.util

import com.mininas.client.data.api.FileInfo
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

object FileTypes {
    private val imageExt = setOf("jpg", "jpeg", "png", "gif", "webp", "bmp", "svg")
    private val videoExt = setOf("mp4", "mkv", "avi", "mov", "webm")
    private val audioExt = setOf("mp3", "wav", "flac", "ogg", "aac", "m4a")
    private val textExt = setOf("txt", "md", "log", "csv", "json", "xml", "yaml", "yml", "ini", "conf", "sh", "py", "js", "ts", "go", "java", "kt", "c", "cpp", "h", "hpp")
    private val pdfExt = setOf("pdf")

    fun ext(name: String): String =
        name.substringAfterLast('.', "").lowercase()

    fun isImage(name: String) = ext(name) in imageExt
    fun isVideo(name: String) = ext(name) in videoExt
    fun isAudio(name: String) = ext(name) in audioExt
    fun isText(name: String) = ext(name) in textExt
    fun isPdf(name: String) = ext(name) in pdfExt
    fun isPreviewable(name: String) =
        isImage(name) || isVideo(name) || isAudio(name) || isText(name) || isPdf(name)

    fun category(file: FileInfo): Category {
        if (file.isDir) return Category.DIR
        val n = file.name
        return when {
            isImage(n) -> Category.IMAGE
            isVideo(n) -> Category.VIDEO
            isAudio(n) -> Category.AUDIO
            isPdf(n) -> Category.PDF
            isText(n) -> Category.TEXT
            else -> Category.OTHER
        }
    }

    enum class Category { DIR, IMAGE, VIDEO, AUDIO, PDF, TEXT, OTHER }
}

object FormatUtil {
    private val dateFmt = SimpleDateFormat("yyyy-MM-dd HH:mm", Locale.getDefault())

    fun size(bytes: Long): String {
        if (bytes < 1024) return "${bytes} B"
        val kb = bytes / 1024.0
        if (kb < 1024) return "%.1f KB".format(kb)
        val mb = kb / 1024.0
        if (mb < 1024) return "%.1f MB".format(mb)
        val gb = mb / 1024.0
        return "%.2f GB".format(gb)
    }

    fun time(epochSeconds: Long): String {
        if (epochSeconds <= 0) return ""
        return dateFmt.format(Date(epochSeconds * 1000))
    }
}

object PathUtil {
    fun parent(path: String): String {
        if (path.isEmpty()) return ""
        val trimmed = path.trimEnd('/')
        val idx = trimmed.lastIndexOf('/')
        return if (idx <= 0) "" else trimmed.substring(0, idx)
    }

    fun join(base: String, name: String): String {
        if (base.isEmpty()) return name
        return if (base.endsWith('/')) "$base$name" else "$base/$name"
    }

    fun basename(path: String): String = path.trimEnd('/').substringAfterLast('/')
}
