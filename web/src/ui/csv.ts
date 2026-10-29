// CSV built in the browser from rows already loaded (design/: "CSV indir"). A byte order
// mark lets spreadsheet tools read Turkish characters; cells are always quoted, and text a
// user wrote (reasons, names) cannot start a spreadsheet formula.
const BOM = String.fromCharCode(0xfeff);

const cell = (c: string | number | null | undefined) => {
  const s = String(c ?? "");
  return `"${(/^[=+\-@\t\r]/.test(s) ? "'" + s : s).replace(/"/g, '""')}"`;
};

export const toCsv = (rows: (string | number | null | undefined)[][]) => BOM + rows.map((r) => r.map(cell).join(",")).join("\r\n");

export function downloadCsv(fileName: string, rows: (string | number | null | undefined)[][]) {
  const url = URL.createObjectURL(new Blob([toCsv(rows)], { type: "text/csv;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = fileName;
  a.click();
  URL.revokeObjectURL(url);
}
