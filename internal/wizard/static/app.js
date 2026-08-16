// Vanilla JS against the wizard's small JSON API — deliberately no
// framework, given the whole UI is a table list, a column detail panel,
// and two buttons.

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
    const h2 = document.createElement("h2");
    h2.textContent = table.Name;
    container.appendChild(h2);

    const tbl = document.createElement("table");
    tbl.innerHTML = `<thead><tr>
      <th>Column</th><th>Declared</th><th>Target</th>
      <th>Confidence</th><th>Source</th><th>Override</th>
    </tr></thead>`;
    const tbody = document.createElement("tbody");

    for (const col of table.Columns || []) {
      const tr = document.createElement("tr");
      if (col.NeedsReview) tr.className = "needs-review";

      tr.innerHTML = `
        <td>${col.Column}</td>
        <td>${col.DeclaredType}</td>
        <td>${col.TargetType}</td>
        <td class="confidence">${col.Confidence.toFixed(2)}</td>
        <td>${col.Source}<div class="rationale">${col.Rationale || ""}</div></td>
        <td>
          <input type="text" value="${col.TargetType}" data-table="${table.Name}" data-column="${col.Column}">
          <button data-action="approve" data-table="${table.Name}" data-column="${col.Column}">Approve</button>
        </td>`;
      tbody.appendChild(tr);
    }
    tbl.appendChild(tbody);
    container.appendChild(tbl);
  }

  container.querySelectorAll('button[data-action="approve"]').forEach((btn) => {
    btn.addEventListener("click", async () => {
      const table = btn.dataset.table;
      const column = btn.dataset.column;
      const input = container.querySelector(
        `input[data-table="${table}"][data-column="${column}"]`
      );
      await fetch(`/api/columns/${table}/${column}/decision`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ target_type: input.value, rationale: "human confirmed via wizard" }),
      });
      loadSummary();
    });
  });
}

document.getElementById("finish").addEventListener("click", async () => {
  await fetch("/api/finish", { method: "POST" });
  document.body.innerHTML = "<h1>Review complete. You can close this tab.</h1>";
});

loadSummary();
