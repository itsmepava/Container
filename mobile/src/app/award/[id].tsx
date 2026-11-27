import * as WebBrowser from 'expo-web-browser';
import { Stack, useLocalSearchParams } from 'expo-router';
import React, { useCallback, useEffect, useState } from 'react';
import { Linking, Platform, RefreshControl, Text, View } from 'react-native';
import { Banner, Body, Button, Card, DocRow, Eyebrow, H1, H2, KV, Loading, Pill, Row, Screen } from '@/components/ui';
import { api, Award } from '@/lib/api';
import { day, dayTime, money } from '@/lib/format';
import { useTheme } from '@/lib/theme';
import { chooseFile } from '../register';

async function openDoc(id: number) {
  const { url } = await api.documentLink(id);
  if (Platform.OS === 'web') globalThis.open?.(url, '_blank');
  else await WebBrowser.openBrowserAsync(url);
}

export default function AwardScreen() {
  const t = useTheme();
  const id = Number(useLocalSearchParams<{ id: string }>().id);
  const [award, setAward] = useState<Award | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState('');
  const [msg, setMsg] = useState('');
  const [refreshing, setRefreshing] = useState(false);

  const load = useCallback(async () => {
    try {
      setAward(await api.award(id));
    } catch (e) {
      setError((e as Error).message);
    }
  }, [id]);
  useEffect(() => {
    void load();
  }, [load]);

  async function upload(kind: 'booking_confirmation' | 'bl_copy') {
    setError('');
    setMsg('');
    try {
      const f = await chooseFile();
      if (!f) return;
      setBusy(kind);
      await api.uploadTenderDoc(id, kind, f);
      setMsg(kind === 'bl_copy' ? 'B/L copy uploaded.' : 'Booking confirmation uploaded. The shippers have been told.');
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy('');
    }
  }

  if (!award) return error ? <Screen><Banner tone="red" text={error} /></Screen> : <Loading />;
  const tn = award.tender;
  const totalKg = award.shippers.reduce((s, x) => s + x.kg, 0);
  return (
    <>
      <Stack.Screen options={{ title: tn.ref }} />
      <Screen refresh={<RefreshControl refreshing={refreshing} onRefresh={async () => { setRefreshing(true); await load(); setRefreshing(false); }} />}>
        <Eyebrow color={t.green}>You won · USD {money(tn.wonAmount ?? 0)}</Eyebrow>
        <H1>{tn.lane} · 1 × {tn.boxCode}</H1>
        <Card>
          <KV k="Vessel" v={tn.vessel ? `${tn.vessel.label} · ${tn.vessel.serviceText}` : '–'} />
          <KV k="ETD · ETA" v={tn.vessel ? `${day(tn.vessel.etd)} · ${day(tn.vessel.eta)}` : '–'} />
          <KV k="CFS cut-off" v={dayTime(tn.cfsCutoff)} />
          {award.originCfs ? <KV k="Origin CFS" v={award.originCfs} /> : null}
          <KV k="Cargo" v={`${tn.cbm.toFixed(1)} CBM · ${(totalKg / 1000).toFixed(1)} t · ${award.shippers.length} shippers`} last />
        </Card>

        <Card tone={award.confirmation && award.blCopy ? 'good' : 'accent'} style={{ gap: 6 }}>
          <H2>Your documents</H2>
          <Body small muted>Due within 24 hours of award. Eimsky invoices the shippers once your booking confirmation is in.</Body>
          <DocRow label="Booking confirmation" sub={award.confirmation ? `${award.confirmation.filename} · ${award.confirmation.size}` : 'Required'} done={!!award.confirmation}
            action={<Button title={award.confirmation ? 'Replace' : 'Upload'} kind={award.confirmation ? 'ghost' : 'primary'} onPress={() => upload('booking_confirmation')} loading={busy === 'booking_confirmation'} style={{ minHeight: 40, paddingHorizontal: 12 }} testID="upload-confirmation" />} />
          <DocRow label="B/L copy" sub={award.blCopy ? `${award.blCopy.filename} · ${award.blCopy.size}` : 'Your bill of lading for this box'} done={!!award.blCopy}
            action={<Button title={award.blCopy ? 'Replace' : 'Upload'} kind={award.blCopy ? 'ghost' : 'secondary'} onPress={() => upload('bl_copy')} loading={busy === 'bl_copy'} style={{ minHeight: 40, paddingHorizontal: 12 }} testID="upload-bl" />} />
          {msg ? <Banner tone="green" text={msg} /> : null}
          {error ? <Banner tone="red" text={error} /> : null}
        </Card>

        <H2 style={{ marginTop: 6 }}>Shippers in this box</H2>
        <Body small muted>Contact them for pickup at the origin CFS. Documents open by secure link and are deleted by Eimsky after handover.</Body>
        {award.shippers.map(s => (
          <Card key={s.bookingRef} style={{ gap: 6 }}>
            <Row style={{ justifyContent: 'space-between' }}>
              <Text style={{ fontSize: 16, fontWeight: '700', color: t.ink, flex: 1 }}>{s.company}</Text>
              <Pill label={`${s.cbm.toFixed(1)} CBM`} />
            </Row>
            <Body small muted>{s.bookingRef} · {s.category} · {Math.round(s.kg).toLocaleString()} kg</Body>
            <Body>{s.contactName}</Body>
            <Row style={{ flexWrap: 'wrap' }}>
              {s.phone ? <Button title={`Call ${s.phone}`} kind="secondary" onPress={() => Linking.openURL(`tel:${s.phone}`)} style={{ minHeight: 40 }} /> : null}
              {s.email ? <Button title="Email" kind="secondary" onPress={() => Linking.openURL(`mailto:${s.email}?subject=${encodeURIComponent(s.bookingRef + ' pickup')}`)} style={{ minHeight: 40 }} /> : null}
            </Row>
            {s.documents.length ? (
              <View>
                {s.documents.map(d => (
                  <DocRow key={d.id} label={d.label} sub={d.deleted ? 'Deleted after handover' : d.filename} done={!d.deleted}
                    action={d.deleted ? undefined : <Button title="Open" kind="ghost" onPress={() => openDoc(d.id).catch(e => setError(e.message))} style={{ minHeight: 40 }} />} />
                ))}
              </View>
            ) : (
              <Body small muted>No documents yet.</Body>
            )}
          </Card>
        ))}
      </Screen>
    </>
  );
}
