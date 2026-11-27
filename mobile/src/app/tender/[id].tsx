import { Stack, useLocalSearchParams, useRouter } from 'expo-router';
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Alert, KeyboardAvoidingView, Platform, RefreshControl, Text, TextInput, View } from 'react-native';
import { Banner, Body, Button, Card, Check, Eyebrow, H1, H2, KV, Loading, Pill, Radio, Row, Screen } from '@/components/ui';
import { api, Tender } from '@/lib/api';
import { day, dayTime, money, parseAmount, until } from '@/lib/format';
import { useTheme } from '@/lib/theme';

export const INCLUSIONS = [
  { key: 'ocean', label: 'Ocean freight' },
  { key: 'origin', label: 'Origin charges and CFS stuffing' },
  { key: 'destination', label: 'Destination THC and delivery order' },
  { key: 'destuff', label: 'De-stuffing at Colombo CFS' },
];

export default function TenderScreen() {
  const t = useTheme();
  const router = useRouter();
  const id = Number(useLocalSearchParams<{ id: string }>().id);
  const [tender, setTender] = useState<Tender | null>(null);
  const [amount, setAmount] = useState('');
  const [vesselId, setVesselId] = useState(0);
  const [inc, setInc] = useState<Record<string, boolean>>({ ocean: true, origin: true, destination: true, destuff: true });
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState('');
  const [refreshing, setRefreshing] = useState(false);
  const [, tick] = useState(0);

  const fill = useCallback((x: Tender) => {
    setTender(x);
    if (x.myBid) {
      setAmount(String(x.myBid.amount));
      setVesselId(x.myBid.vesselId);
      setInc(x.myBid.includes);
      setNote(x.myBid.note);
    } else {
      setVesselId(x.vessel?.id ?? 0);
    }
  }, []);

  const load = useCallback(async () => {
    try {
      fill(await api.tender(id));
    } catch (e) {
      setError((e as Error).message);
    }
  }, [id, fill]);

  useEffect(() => {
    void load();
    const h = setInterval(() => tick(n => n + 1), 30000);
    return () => clearInterval(h);
  }, [load]);

  const value = parseAmount(amount);
  const allIncluded = INCLUSIONS.every(i => inc[i.key]);
  const altVessel = tender && vesselId !== 0 && vesselId !== tender.vessel?.id;
  const changed = useMemo(() => {
    const b = tender?.myBid;
    if (!b) return true;
    return b.amount !== value || b.vesselId !== vesselId || b.note !== note || INCLUSIONS.some(i => !!b.includes[i.key] !== !!inc[i.key]);
  }, [tender, value, vesselId, note, inc]);

  if (!tender) return error ? <Screen><Banner tone="red" text={error} /></Screen> : <Loading />;

  async function submit() {
    setError('');
    setSaved('');
    setBusy(true);
    try {
      const x = await api.bid(id, { amount: value, vesselId, includes: inc, note });
      fill(x);
      setSaved(`Sealed bid of USD ${money(value)} saved. You can change it until ${dayTime(x.closesAt)}.`);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  function withdraw() {
    const go = async () => {
      setBusy(true);
      try {
        fill(await api.withdraw(id));
        setAmount('');
        setSaved('Bid withdrawn.');
      } catch (e) {
        setError((e as Error).message);
      } finally {
        setBusy(false);
      }
    };
    if (Platform.OS === 'web') return void go();
    Alert.alert('Withdraw your bid?', 'You can bid again while the tender is open.', [{ text: 'Keep bid', style: 'cancel' }, { text: 'Withdraw', style: 'destructive', onPress: go }]);
  }

  const v = tender.vessel;
  return (
    <KeyboardAvoidingView style={{ flex: 1 }} behavior={Platform.OS === 'ios' ? 'padding' : undefined} keyboardVerticalOffset={90}>
      <Stack.Screen options={{ title: `${tender.origin} → ${tender.dest} · ${tender.boxCode}` }} />
      <Screen refresh={<RefreshControl refreshing={refreshing} onRefresh={async () => { setRefreshing(true); await load(); setRefreshing(false); }} />}>
        <Row style={{ justifyContent: 'space-between' }}>
          <Eyebrow>{tender.ref}</Eyebrow>
          {tender.open ? <Pill label={`Closes in ${until(tender.closesAt)}`} tone="amber" /> : tender.result === 'won' ? <Pill label="You won" tone="green" /> : tender.result === 'lost' ? <Pill label="Not won" /> : <Pill label="Bids closed" />}
        </Row>
        <H1>{tender.lane} · {tender.boxCode}</H1>
        <Body muted>Closes {dayTime(tender.closesAt)}</Body>

        <Card>
          <KV k="Vessel · ETD" v={v ? `${v.label} · ${day(v.etd)} · ${v.serviceText}` : '–'} />
          <KV k="Cargo" v={`${tender.cbm.toFixed(1)} CBM · ${tender.tonnes.toFixed(1)} t · ${tender.lots} lots`} />
          <KV k="Categories" v={tender.categories} />
          <KV k="CFS cut-off" v={dayTime(tender.cfsCutoff)} />
          {tender.originCfs ? <KV k="Origin CFS" v={tender.originCfs} last /> : null}
        </Card>

        {tender.result === 'won' ? (
          <Card tone="good" style={{ gap: 10 }}>
            <H2>You won at USD {money(tender.wonAmount ?? 0)}</H2>
            <Body>Shipper contacts and documents are ready. Upload your booking confirmation.</Body>
            <Button title="Open awarded box" onPress={() => router.replace(`/award/${tender.id}`)} />
          </Card>
        ) : !tender.open ? (
          <Card>
            <H2>{tender.result === 'lost' ? 'Awarded to another forwarder' : 'Bidding has closed'}</H2>
            <Body muted style={{ marginTop: 6 }}>{tender.myBid ? `Your sealed bid was USD ${money(tender.myBid.amount)}. ` : ''}{tender.result === 'lost' ? "Thanks for bidding. The winning price isn't shared." : 'Eimsky will announce the result soon.'}</Body>
          </Card>
        ) : (
          <>
            <Card style={{ gap: 10 }}>
              <Text style={{ fontSize: 13, fontWeight: '600', color: t.ink2 }}>Your all-in price per container (USD)</Text>
              <TextInput
                value={amount}
                onChangeText={s => setAmount(s.replace(/[^\d.,]/g, ''))}
                keyboardType="decimal-pad"
                placeholder="2,350"
                placeholderTextColor={t.faint}
                accessibilityLabel="All-in price per container in US dollars"
                testID="amount"
                style={{ fontSize: 34, fontWeight: '800', color: t.ink, borderBottomWidth: 2, borderColor: t.primary, paddingVertical: 6 }}
              />
              {amount && isNaN(value) ? <Text style={{ color: t.red }}>Enter a number, like 2350 or 2,350.50</Text> : null}
            </Card>

            <Card style={{ gap: 4 }}>
              <H2>Included in the price (all required)</H2>
              {INCLUSIONS.map(i => <Check key={i.key} label={i.label} checked={!!inc[i.key]} onToggle={() => setInc(s => ({ ...s, [i.key]: !s[i.key] }))} />)}
              {!allIncluded ? <Banner tone="amber" text="Bids that leave out a charge are marked not compliant and won't be picked automatically." /> : null}
            </Card>

            {(tender.vessels?.length ?? 0) > 1 ? (
              <Card style={{ gap: 8 }}>
                <H2>Vessel</H2>
                {tender.vessels!.map(x => (
                  <Radio key={x.id} selected={vesselId === x.id} onPress={() => setVesselId(x.id)} badge={x.id === tender.vessel?.id ? 'Requested' : undefined}
                    label={`${x.label} · ${x.serviceText}`} sub={`ETD ${day(x.etd)} · ETA ${day(x.eta)} · ${x.transitDays} days${x.via ? ` · via ${x.via}` : ''}`} />
                ))}
                {altVessel ? <Banner tone="amber" text="Quoting another vessel makes the bid an alternative: Eimsky sees it, but the auto pick only takes bids on the requested vessel." /> : null}
              </Card>
            ) : null}

            <Card>
              <Text style={{ fontSize: 13, fontWeight: '600', color: t.ink2, marginBottom: 6 }}>Note to Eimsky (optional)</Text>
              <TextInput value={note} onChangeText={setNote} placeholder="e.g. Weekly direct service, free time 7 days" placeholderTextColor={t.faint} multiline maxLength={500}
                style={{ minHeight: 70, color: t.ink, fontSize: 15, borderWidth: 1, borderColor: t.inputLine, borderRadius: 10, padding: 10, textAlignVertical: 'top', backgroundColor: t.input }} />
            </Card>

            <Body small muted>Sealed bid: other forwarders can't see your price. You can change it until the tender closes.</Body>
            {error ? <Banner tone="red" text={error} /> : null}
            {saved ? <Banner tone="green" text={saved} /> : null}
            <Button title={isNaN(value) || !amount ? 'Enter your price' : `${tender.myBid ? 'Update bid' : 'Submit bid'} · USD ${money(value)}`} onPress={submit} loading={busy} disabled={!amount || isNaN(value) || value <= 0 || !changed} testID="submit-bid" />
            {tender.myBid ? <Button title="Withdraw bid" kind="danger" onPress={withdraw} disabled={busy} /> : null}
          </>
        )}
        {error && tender.open === false ? <Banner tone="red" text={error} /> : null}
        <View style={{ height: 8 }} />
      </Screen>
    </KeyboardAvoidingView>
  );
}
