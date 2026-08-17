// Vanilla JS against the wizard's small JSON API — deliberately no
// framework. Renders a real data-preview grid (rows x columns, like a
// spreadsheet/CSV import tool) with the type decision living in each
// column's sticky header: a dropdown of common Postgres types, pre-selected
// to the profiler's best guess, plus a colored top edge for needs-review
// vs auto-approved. Confidence and the heuristic's rationale are available
// on hover (the header's title attribute) rather than as a separate column.

const TYPE_OPTIONS = [
  "text", "integer", "bigint", "smallint", "boolean",
  "double precision", "real", "numeric",
  "date", "timestamptz", "jsonb", "bytea",
];

async function loadSummary() {
  const res = await fetch("/api/summary");
  const summary = await res.json();
  render(summary);
}

function render(summary) {
  document.getElementById("counts").textContent =
    `${summary.NeedsReviewCount} column(s) need review, ${summary.AutoApprovedCount} auto-approved`;

  const container = document.getElementById("tables");
  container.innerHTML = "";

  for (const table of summary.Tables || []) {
    container.appendChild(renderTable(table));
  }
}

function renderTable(table) {
  const panel = document.createElement("div");
  panel.className = "panel";

  const needsReview = (table.Columns || []).filter((c) => c.NeedsReview).length;
  const toolbar = document.createElement("div");
  toolbar.className = "toolbar";
  toolbar.innerHTML = `
    <div class="title">${table.Name}</div>
    <div class="counts">${(table.RowCount || 0).toLocaleString()} row(s) &middot; ${needsReview} need review</div>`;
  panel.appendChild(toolbar);

  const gridwrap = document.createElement("div");
  gridwrap.className = "gridwrap";

  if (!table.Columns || table.Columns.length === 0) {
    gridwrap.innerHTML = `<div class="empty">No columns to load for this table.</div>`;
    panel.appendChild(gridwrap);
    return panel;
  }

  const grid = document.createElement("table");
  grid.className = "grid";

  const thead = document.createElement("thead");
  const headRow = document.createElement("tr");
  headRow.innerHTML = `<th class="rownum-head"></th>`;
  for (const col of table.Columns) {
    headRow.appendChild(renderHeaderCell(table.Name, col));
  }
  thead.appendChild(headRow);
  grid.appendChild(thead);

  const tbody = document.createElement("tbody");
  const rows = table.Rows || [];
  if (rows.length === 0) {
    const tr = document.createElement("tr");
    const td = document.createElement("td");
    td.className = "empty";
    td.colSpan = table.Columns.length + 1;
    td.textContent = "No preview data available (source file not reachable for sampling).";
    tr.appendChild(td);
    tbody.appendChild(tr);
  } else {
    rows.forEach((row, i) => {
      const tr = document.createElement("tr");
      const rownum = document.createElement("td");
      rownum.className = "rownum";
      rownum.textContent = String(i + 1);
      tr.appendChild(rownum);
      for (const cell of row) {
        const td = document.createElement("td");
        td.className = "cell";
        td.textContent = cell;
        tr.appendChild(td);
      }
      tbody.appendChild(tr);
    });
  }
  grid.appendChild(tbody);

  gridwrap.appendChild(grid);
  panel.appendChild(gridwrap);
  return panel;
}

function renderHeaderCell(tableName, col) {
  const th = document.createElement("th");
  th.className = "colhead " + (col.NeedsReview ? "needs-review" : "auto");
  th.title = buildTooltip(col);

  const box = document.createElement("div");
  box.className = "headbtn";

  const name = document.createElement("span");
  name.className = "name";
  name.textContent = col.Column;
  box.appendChild(name);

  const declared = document.createElement("span");
  declared.className = "declared";
  declared.textContent = col.DeclaredType || "(none)";
  box.appendChild(declared);

  const select = document.createElement("select");
  select.dataset.table = tableName;
  select.dataset.column = col.Column;
  const options = TYPE_OPTIONS.includes(col.TargetType)
    ? TYPE_OPTIONS
    : [col.TargetType, ...TYPE_OPTIONS];
  for (const opt of options) {
    const o = document.createElement("option");
    o.value = opt;
    o.textContent = opt;
    if (opt === col.TargetType) o.selected = true;
    select.appendChild(o);
  }
  select.addEventListener("click", (e) => e.stopPropagation());
  select.addEventListener("change", async () => {
    // transform is intentionally left blank (passthrough): a manual
    // override doesn't carry over whatever transform the original
    // heuristic guess implied (e.g. int_to_bool no longer applies once
    // the target type is changed away from boolean).
    await fetch(`/api/columns/${tableName}/${col.Column}/decision`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        target_type: select.value,
        transform: "",
        rationale: "human confirmed via wizard",
      }),
    });
    loadSummary();
  });
  box.appendChild(select);

  th.appendChild(box);
  return th;
}

function buildTooltip(col) {
  const pct = Math.round((col.Confidence || 0) * 100);
  const lines = [`${pct}% confidence (${col.Source || "unknown"})`];
  if (col.Rationale) lines.push(col.Rationale);
  return lines.join("\n");
}

document.getElementById("finish").addEventListener("click", async () => {
  await fetch("/api/finish", { method: "POST" });
  document.body.innerHTML = "<h1 style='font-family:system-ui,sans-serif;padding:2rem'>Confirmed. You can close this tab.</h1>";
});

document.getElementById("cancel").addEventListener("click", async () => {
  if (!confirm("Cancel this import? Nothing will be loaded into Postgres.")) return;
  await fetch("/api/cancel", { method: "POST" });
  document.body.innerHTML = "<h1 style='font-family:system-ui,sans-serif;padding:2rem'>Cancelled. Nothing was imported. You can close this tab.</h1>";
});

loadSummary();
