import { ago, day, dayTime, money, parseAmount, until } from '@/lib/format';

describe('format', () => {
  it('shows Colombo dates', () => {
    // 2026-11-30 11:30 UTC is 17:00 in Colombo.
    expect(dayTime('2026-11-30T11:30:00Z')).toBe('Mon 30 Nov 17:00');
    expect(day('2026-12-02T14:30:00Z')).toBe('Wed 2 Dec');
  });
  it('counts down', () => {
    const now = new Date('2026-11-27T10:00:00Z');
    expect(until('2026-11-28T16:00:00Z', now)).toBe('1 d 6 h');
    expect(until('2026-12-02T10:00:00Z', now)).toBe('5 d');
    expect(until('2026-11-27T10:39:30Z', now)).toBe('40 min');
    expect(until('2026-11-27T09:00:00Z', now)).toBe('closed');
  });
  it('formats and parses money', () => {
    expect(money(2350)).toBe('2,350');
    expect(money(320.21)).toBe('320.21');
    expect(parseAmount('2,350')).toBe(2350);
    expect(parseAmount('2350.5')).toBe(2350.5);
    expect(parseAmount('')).toBeNaN();
    expect(parseAmount('12.345')).toBeNaN();
    expect(parseAmount('abc')).toBeNaN();
  });
  it('says how long ago', () => {
    const now = new Date('2026-11-27T10:00:00Z');
    expect(ago('2026-11-27T09:59:30Z', now)).toBe('just now');
    expect(ago('2026-11-27T07:00:00Z', now)).toBe('3 h ago');
  });
});
