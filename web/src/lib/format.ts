export const fmtCpu = (m: number): string =>
  m >= 1000 ? `${(m / 1000).toFixed(2)} cores` : `${Math.round(m)} m`;

export const fmtMem = (b: number): string => {
  const u = ["B", "KiB", "MiB", "GiB", "TiB"];
  let i = 0, v = b;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${u[i]}`;
};

export const fmtTime = (unix: number): string =>
  new Date(unix * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });

// status color ramp for utilization percentages
export const usageColor = (p: number): string =>
  p >= 85 ? "#f43f5e" : p >= 65 ? "#f59e0b" : "#10b981";

export const usageTone = (p: number): "ok" | "warn" | "crit" =>
  p >= 85 ? "crit" : p >= 65 ? "warn" : "ok";
