import React, { useCallback, useEffect, useState } from 'react';
import { ActionSheetIOS, Alert, Platform, View } from 'react-native';
import { Banner, Body, Button, Card, Check, DocRow, Eyebrow, Field, H1, H2, Row, Screen } from '@/components/ui';
import { api, ApiError, Doc, Lane, PickedFile } from '@/lib/api';
import { useAuth } from '@/lib/auth';
import { pickDocument, takePhoto } from '@/lib/files';
import { useTheme } from '@/lib/theme';

const DOCS = [
  { kind: 'br_certificate', label: 'Business registration', help: 'Certificate of incorporation or BR', required: true },
  { kind: 'ff_licence', label: 'Freight forwarder licence', help: 'Photo or PDF', required: true },
  { kind: 'origin_agent', label: 'Origin agent in China or India', help: 'Optional', required: false },
];

/** Asks whether to take a photo or choose a file, then returns the file. */
export async function chooseFile(): Promise<PickedFile | null> {
  if (Platform.OS === 'web') return pickDocument();
  const choice = await new Promise<number>(resolve => {
    if (Platform.OS === 'ios') {
      ActionSheetIOS.showActionSheetWithOptions({ options: ['Take a photo', 'Choose a file', 'Cancel'], cancelButtonIndex: 2 }, resolve);
    } else {
      Alert.alert('Add a document', undefined, [
        { text: 'Take a photo', onPress: () => resolve(0) },
        { text: 'Choose a file', onPress: () => resolve(1) },
        { text: 'Cancel', style: 'cancel', onPress: () => resolve(2) },
      ]);
    }
  });
  if (choice === 0) return takePhoto();
  if (choice === 1) return pickDocument();
  return null;
}

export default function Register() {
  const t = useTheme();
  const { me, refresh, signOut } = useAuth();
  const [step, setStep] = useState(1);
  const [company, setCompany] = useState(me?.company?.name ?? '');
  const [reg, setReg] = useState(me?.company?.regNumber ?? '');
  const [contact, setContact] = useState(me?.user.name ?? '');
  const [email, setEmail] = useState(me?.user.email ?? '');
  const [docs, setDocs] = useState<Doc[]>([]);
  const [lanes, setLanes] = useState<Lane[]>([]);
  const [picked, setPicked] = useState<number[]>(me?.company?.laneIds ?? []);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');

  const loadDocs = useCallback(async () => {
    try {
      setDocs(await api.myDocuments());
    } catch {}
  }, []);

  // Fill the form once the profile arrives.
  useEffect(() => {
    if (!me) return;
    setCompany(c => c || me.company?.name || '');
    setReg(r => r || me.company?.regNumber || '');
    setContact(c => c || me.user.name || '');
    setEmail(e => e || me.user.email || '');
    setPicked(p => (p.length ? p : me.company?.laneIds ?? []));
  }, [me]);

  useEffect(() => {
    void loadDocs();
    api.lanes().then(setLanes).catch(() => {});
  }, [loadDocs]);

  async function run(key: string, fn: () => Promise<void>) {
    setError('');
    setBusy(key);
    try {
      await fn();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy('');
    }
  }

  const saveCompany = () =>
    run('company', async () => {
      await api.saveProfile({ companyName: company, regNumber: reg, contactName: contact, email });
      await refresh();
      setStep(2);
    });

  const upload = (kind: string) =>
    run(kind, async () => {
      const f = await chooseFile();
      if (!f) return;
      await api.uploadCompanyDoc(kind, f);
      await loadDocs();
    });

  const submit = () =>
    run('submit', async () => {
      await api.setLanes(picked);
      await api.submit();
      await refresh();
    });

  const has = (kind: string) => docs.find(d => d.kind === kind);
  const docsOK = DOCS.filter(d => d.required).every(d => has(d.kind));

  return (
    <Screen>
      <Eyebrow>Eimsky · Forwarder</Eyebrow>
      <H1>Register to bid on FCL</H1>
      <Body muted>Step {step} of 3 · {step === 1 ? 'Company details' : step === 2 ? 'Company documents' : 'Your lanes'}</Body>
      <Row>{[1, 2, 3].map(i => <View key={i} style={{ flex: 1, height: 4, borderRadius: 2, backgroundColor: i <= step ? t.primary : t.line }} />)}</Row>
      {me?.user.companyStatus === 'rejected' && me.registration.note ? <Banner tone="amber" text={`Eimsky asked for changes: ${me.registration.note}`} /> : null}

      {step === 1 && (
        <Card style={{ gap: 14 }}>
          <Field label="Company name" value={company} onChangeText={setCompany} placeholder="Blue Harbour Logistics" autoCapitalize="words" testID="company" />
          <Field label="Business registration no. (optional)" value={reg} onChangeText={setReg} autoCapitalize="characters" />
          <Field label="Your name" value={contact} onChangeText={setContact} autoCapitalize="words" autoComplete="name" testID="contact" />
          <Field label="Email (optional)" value={email} onChangeText={setEmail} keyboardType="email-address" autoCapitalize="none" autoComplete="email" help="For award emails and receipts." />
          <Field label="Contact mobile · verified" value={me?.user.phone ?? ''} editable={false} />
          {error ? <Banner tone="red" text={error} /> : null}
          <Button title="Next: company documents" onPress={saveCompany} loading={busy === 'company'} disabled={!company.trim() || !contact.trim()} testID="next-1" />
        </Card>
      )}

      {step === 2 && (
        <Card>
          <H2 style={{ marginBottom: 4 }}>Company documents</H2>
          <Body small muted>PDF, JPG or PNG, up to 10 MB. You can take a photo.</Body>
          {DOCS.map(d => {
            const doc = has(d.kind);
            return (
              <DocRow key={d.kind} label={d.label} sub={doc ? doc.filename : d.help} done={!!doc}
                action={<Button title={doc ? 'Replace' : d.required ? 'Upload' : 'Add'} kind={doc ? 'ghost' : 'secondary'} onPress={() => upload(d.kind)} loading={busy === d.kind} style={{ minHeight: 40, paddingHorizontal: 12 }} />} />
            );
          })}
          {error ? <View style={{ marginTop: 12 }}><Banner tone="red" text={error} /></View> : null}
          <View style={{ gap: 8, marginTop: 16 }}>
            <Button title="Next: choose your lanes" onPress={() => setStep(3)} disabled={!docsOK} testID="next-2" />
            <Button title="Back" kind="ghost" onPress={() => setStep(1)} />
          </View>
        </Card>
      )}

      {step === 3 && (
        <Card>
          <H2>Which lanes do you serve?</H2>
          <Body small muted>You get a push notification for every new FCL tender on these lanes.</Body>
          <View style={{ marginTop: 8 }}>
            {lanes.map(l => (
              <Check key={l.id} label={l.label} sub={`${l.origin} → ${l.dest}`} checked={picked.includes(l.id)}
                onToggle={() => setPicked(p => (p.includes(l.id) ? p.filter(x => x !== l.id) : [...p, l.id]))} />
            ))}
          </View>
          {error ? <Banner tone="red" text={error} /> : null}
          <View style={{ gap: 8, marginTop: 12 }}>
            <Button title="Submit for approval" onPress={submit} loading={busy === 'submit'} disabled={picked.length === 0} testID="submit" />
            <Button title="Back" kind="ghost" onPress={() => setStep(2)} />
          </View>
          <Body small muted style={{ marginTop: 8, textAlign: 'center' }}>Eimsky approves new forwarders within one working day.</Body>
        </Card>
      )}
      <Button title="Sign out" kind="ghost" onPress={signOut} />
    </Screen>
  );
}
