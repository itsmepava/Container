// Formatting helpers. Times are shown in Colombo time (UTC+5:30).
const OFFSET_MIN = 330;

function colombo(d: Date | string): Date {
  const t = typeof d === 'string' ? new Date(d) : d;
  return new Date(t.getTime() + (OFFSET_MIN + t.getTimezoneOffset()) * 60000);
}

const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

export function day(d: Date | string): string {
  const c = colombo(d);
  return `${DAYS[c.getDay()]} ${c.getDate()} ${MONTHS[c.getMonth()]}`;
}

export function dayTime(d: Date | string): string {
  const c = colombo(d);
  const hh = String(c.getHours()).padStart(2, '0');
  const mm = String(c.getMinutes()).padStart(2, '0');
  return `${day(d)} ${hh}:${mm}`;
}

/** "5 d", "1 d 6 h", "40 min", or "closed". */
export function until(d: Date | string, now: Date = new Date()): string {
  const s = (new Date(d).getTime() - now.getTime()) / 1000;
  if (s <= 0) return 'closed';
  const days = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (days >= 2) return `${days} d`;
  if (days >= 1) return `${days} d ${h} h`;
  if (h >= 1) return `${h} h ${m} min`;
  return `${m + 1} min`;
}

export function usd(n: number, decimals = 0): string {
  return n.toLocaleString('en-US', { minimumFractionDigits: decimals, maximumFractionDigits: decimals });
}

export function money(n: number): string {
  return Number.isInteger(n) ? usd(n) : usd(n, 2);
}

/** Parses "2,350" or "2350.50" into a number; NaN when empty or invalid. */
export function parseAmount(s: string): number {
  const clean = s.replace(/[,\s]/g, '');
  if (!/^\d+(\.\d{0,2})?$/.test(clean)) return NaN;
  return parseFloat(clean);
}

export function ago(d: Date | string, now: Date = new Date()): string {
  const s = (now.getTime() - new Date(d).getTime()) / 1000;
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  if (s < 7 * 86400) return `${Math.floor(s / 86400)} d ago`;
  return day(d);
}
