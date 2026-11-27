import React from 'react';
import { Body } from '@/components/ui';
import { TenderList } from '@/components/TenderList';

export default function Awarded() {
  return (
    <TenderList
      scope="awarded"
      hrefFor={t => `/award/${t.id}`}
      header={<Body muted>Containers you've won. Open one for shipper contacts and documents, and upload your booking confirmation.</Body>}
      empty={{ title: 'Nothing awarded yet', body: 'When you win a box it appears here with everything you need for pickup.' }}
    />
  );
}
