package com.mininas.client.ui.files

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

@Composable
fun SortBar(
    sortBy: SortBy,
    sortDesc: Boolean,
    total: Int,
    onSort: (SortBy) -> Unit,
) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 2.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        SortChip("名称", sortBy == SortBy.NAME, sortDesc) { onSort(SortBy.NAME) }
        SortChip("大小", sortBy == SortBy.SIZE, sortDesc) { onSort(SortBy.SIZE) }
        SortChip("时间", sortBy == SortBy.TIME, sortDesc) { onSort(SortBy.TIME) }
        Text(
            "$total 项",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(start = 8.dp),
        )
    }
}

@Composable
private fun SortChip(label: String, active: Boolean, desc: Boolean, onClick: () -> Unit) {
    val arrow = if (active) (if (desc) " ↑" else " ↓") else ""
    TextButton(
        onClick = onClick,
        contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = 8.dp, vertical = 0.dp),
    ) {
        Text(
            label + arrow,
            style = MaterialTheme.typography.labelMedium,
            color = if (active) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}
