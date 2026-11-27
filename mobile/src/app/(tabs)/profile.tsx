import { useFocusEffect } from 'expo-router';
import React, { useCallback, useEffect, useState } from 'react';
import { Platform } from 'react-native';
import { Banner, Body, Button, Card, Check, Field, H2, KV, Screen } from '@/components/ui';
import { api, Lane } from '@/lib/api';
import { useAuth } from '@/lib/auth';
import { registerForPush } from '@/lib/push';

export default function Profile() {
  const { me, refresh, signOut } = useAuth();
  const [name, setName] = useState(me?.user.name ?? '');
  const [email, setEmail] = useState(me?.user.email ?? '');
  const [lanes, setLanes] = useState<Lane[]>([]);
  const [picked, setPicked] = useState<number[]>(me?.company?.laneIds ?? []);
  const [msg, setMsg] = useState<{ tone: 'green' | 'red' | 'amber'; text: string } | null>(null);
  const [busy, setBusy] = useState('');

  useEffect(() => {
    api.lanes().then(setLanes).catch(() => {});
  }, []);
  useFocusEffect(useCallback(() => { void refresh(); }, [refresh]));
  useEffect(() => {
    setPicked(me?.company?.laneIds ?? []);
  }, [me?.company?.laneIds]);

  async function run(key: string, fn: () => Promise<string>) {
    setBusy(key);
    setMsg(null);
    try {
      setMsg({ tone: 'green', text: await fn() });
    } catch (e) {
      setMsg({ tone: 'red', text: (e as Error).message });
    } finally {
      setBusy('');
    }
  }

  return (
    <Screen>
      <Card>
        <KV k="Company" v={me?.user.companyName ?? ''} />
        <KV k="Mobile" v={me?.user.phone ?? ''} />
        <KV k="Status" v={me?.user.companyStatus === 'approved' ? 'Approved' : me?.user.companyStatus ?? ''} last />
      </Card>
      {msg ? <Banner tone={msg.tone} text={msg.text} /> : null}
      <Card style={{ gap: 12 }}>
        <H2>Your details</H2>
        <Field label="Name" value={name} onChangeText={setName} autoCapitalize="words" />
        <Field label="Email" value={email} onChangeText={setEmail} keyboardType="email-address" autoCapitalize="none" help="Award emails go here." />
        <Button title="Save details" kind="secondary" loading={busy === 'details'}
          onPress={() => run('details', async () => {
            await api.saveProfile({ companyName: me?.company?.name ?? me?.user.companyName ?? '', regNumber: me?.company?.regNumber ?? '', contactName: name, email });
            await refresh();
            return 'Details saved.';
          })} />
      </Card>
      <Card>
        <H2>Lanes you serve</H2>
        <Body small muted>New tenders on these lanes come straight to your phone.</Body>
        {lanes.map(l => (
          <Check key={l.id} label={l.label} checked={picked.includes(l.id)} onToggle={() => setPicked(p => (p.includes(l.id) ? p.filter(x => x !== l.id) : [...p, l.id]))} />
        ))}
        <Button title="Save lanes" kind="secondary" loading={busy === 'lanes'} disabled={!picked.length} style={{ marginTop: 8 }}
          onPress={() => run('lanes', async () => { await api.setLanes(picked); await refresh(); return 'Lanes saved.'; })} />
      </Card>
      {Platform.OS !== 'web' ? (
        <Button title="Turn on tender notifications" kind="secondary" loading={busy === 'push'}
          onPress={() => run('push', async () => { const why = await registerForPush(); if (why) throw new Error(why); return 'Notifications are on for this phone.'; })} />
      ) : null}
      <Button title="Sign out" kind="danger" onPress={signOut} />
      <Body small muted style={{ textAlign: 'center' }}>Eimsky Forwarder · times shown in Colombo time</Body>
    </Screen>
  );
}
