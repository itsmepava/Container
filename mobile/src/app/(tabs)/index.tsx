import React from 'react';
import { Body } from '@/components/ui';
import { TenderList } from '@/components/TenderList';
import { useAuth } from '@/lib/auth';

export default function OpenTenders() {
  const { me } = useAuth();
  return (
    <TenderList
      scope="open"
      header={<Body muted>Your lanes · shipper names are shared only with the winning forwarder after award.</Body>}
      empty={{ title: 'No open tenders right now', body: `We'll send a push notification the moment a container on your lanes opens for bidding${me?.company?.laneIds?.length ? '' : '. Choose your lanes in Profile'}.` }}
    />
  );
}
