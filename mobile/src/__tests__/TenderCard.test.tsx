import { render, screen } from '@testing-library/react-native';
import React from 'react';
import { TenderCard } from '@/components/TenderCard';
import type { Tender } from '@/lib/api';

jest.mock('expo-router', () => ({ useRouter: () => ({ push: jest.fn() }) }));

const tender: Tender = {
  id: 7, ref: 'TND-49-0021', lane: 'Shanghai → Colombo', origin: 'Shanghai', dest: 'Colombo', week: 49, boxCode: '40HC',
  cbm: 58, tonnes: 12.4, lots: 9, categories: 'Fabric', closesAt: new Date(Date.now() + 30 * 3600e3).toISOString(),
  cfsCutoff: new Date().toISOString(), status: 'open', open: true, vessel: null, myBid: null, result: '', originCfs: '',
};

describe('TenderCard', () => {
  it('shows a new open tender', async () => {
    await render(<TenderCard tender={tender} />);
    expect(screen.getByText('Shanghai → Colombo')).toBeTruthy();
    expect(screen.getByText('58.0 CBM')).toBeTruthy();
    expect(screen.getByText('NEW')).toBeTruthy();
    expect(screen.getByText(/Closes in 1 d 5 h|Closes in 1 d 6 h/)).toBeTruthy();
  });
  it('shows a won tender', async () => {
    await render(<TenderCard tender={{ ...tender, open: false, status: 'awarded', result: 'won', wonAmount: 2280, myBid: { id: 1, amount: 2280, vesselId: 1, includes: {}, note: '', submittedAt: '', updatedAt: '' } }} />);
    expect(screen.getByText('AWARDED')).toBeTruthy();
    expect(screen.getByText(/You won at USD 2,280/)).toBeTruthy();
  });
});
