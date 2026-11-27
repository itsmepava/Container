import React from 'react';
import { Body } from '@/components/ui';
import { TenderList } from '@/components/TenderList';

export default function MyBids() {
  return (
    <TenderList
      scope="mine"
      header={<Body muted>Every tender you've bid on. Results show once Eimsky awards the box; other bidders' prices are never shared.</Body>}
      empty={{ title: 'No bids yet', body: 'Open a tender and place a sealed all-in bid.' }}
    />
  );
}
