import { useRouter } from 'expo-router';
import React, { useCallback, useState } from 'react';
import { RefreshControl } from 'react-native';
import { Banner, Body, Button, Card, Eyebrow, H1, KV, Screen } from '@/components/ui';
import { useAuth } from '@/lib/auth';

export default function Pending() {
  const { me, refresh, signOut } = useAuth();
  const router = useRouter();
  const [refreshing, setRefreshing] = useState(false);
  const onRefresh = useCallback(async () => {
    setRefreshing(true);
    await refresh();
    setRefreshing(false);
  }, [refresh]);
  const status = me?.user.companyStatus;
  return (
    <Screen refresh={<RefreshControl refreshing={refreshing} onRefresh={onRefresh} />}>
      <Eyebrow>{me?.user.companyName}</Eyebrow>
      <H1>{status === 'suspended' ? 'Your account is suspended' : 'Thanks, we’re checking your details'}</H1>
      {status === 'suspended' ? (
        <Banner tone="red" text={me?.registration.note || 'Contact Eimsky to restore access.'} />
      ) : (
        <Body muted>Eimsky approves new forwarders within one working day. We'll send a notification as soon as you can bid. Pull down to check again.</Body>
      )}
      <Card>
        <KV k="Company details" v="Done" />
        <KV k="Business registration" v={me?.registration.hasBR ? 'Uploaded' : 'Missing'} />
        <KV k="Forwarder licence" v={me?.registration.hasLicence ? 'Uploaded' : 'Missing'} />
        <KV k="Lanes" v={String(me?.registration.lanes ?? 0)} last />
      </Card>
      {status !== 'suspended' ? <Button title="Edit registration" kind="secondary" onPress={() => router.push('/register')} /> : null}
      <Button title="Check again" onPress={onRefresh} loading={refreshing} />
      <Button title="Sign out" kind="ghost" onPress={signOut} />
    </Screen>
  );
}
