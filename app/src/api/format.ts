export function formatBytes(n: number): string {
  if (!n || n < 0) return '0 B';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

export function relativeTime(unixSec: number | undefined | null): string {
  if (!unixSec) return 'never';
  const now = Math.floor(Date.now() / 1000);
  const d = now - unixSec;
  if (d < 0) return 'future';
  if (d < 5) return 'just now';
  if (d < 60) return `${d}s ago`;
  if (d < 3600) return `${Math.floor(d / 60)}m ago`;
  if (d < 86400) return `${Math.floor(d / 3600)}h ago`;
  return `${Math.floor(d / 86400)}d ago`;
}

export function formatTimestamp(unixSec: number | undefined | null): string {
  if (!unixSec) return '—';
  return new Date(unixSec * 1000).toLocaleString();
}

export function isHandshakeLive(latest: number | undefined | null): boolean {
  if (!latest) return false;
  return Math.floor(Date.now() / 1000) - latest < 180;
}

export function formatRate(bytesPerSec: number): string {
  if (!bytesPerSec || bytesPerSec < 1) return '0';
  return `${formatBytes(bytesPerSec)}/s`;
}
