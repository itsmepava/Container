import { homeFor } from '@/lib/auth';
import type { Me } from '@/lib/api';

const base = (status: Me['user']['companyStatus'], submitted: boolean): Me => ({
  user: { id: 1, name: 'A', phone: '+94770000001', email: '', companyId: status === 'none' ? 0 : 5, companyName: 'X', companyStatus: status },
  registration: { hasCompany: true, hasBR: true, hasLicence: true, hasAgent: false, lanes: 1, submitted, status, note: '', readyToSubmit: true },
});

describe('homeFor', () => {
  it('routes by approval state', () => {
    expect(homeFor(base('approved', true))).toBe('/(tabs)');
    expect(homeFor(base('pending', true))).toBe('/pending');
    expect(homeFor(base('pending', false))).toBe('/register');
    expect(homeFor(base('none', false))).toBe('/register');
    expect(homeFor(base('rejected', true))).toBe('/register');
    expect(homeFor(base('suspended', true))).toBe('/pending');
  });
});
