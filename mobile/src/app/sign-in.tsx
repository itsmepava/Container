import React, { useState } from 'react';
import { KeyboardAvoidingView, Platform, Text, View } from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { Banner, Body, Button, Eyebrow, Field, H1 } from '@/components/ui';
import { api, ApiError } from '@/lib/api';
import { useAuth } from '@/lib/auth';
import { useTheme } from '@/lib/theme';

export default function SignIn() {
  const t = useTheme();
  const { signIn } = useAuth();
  const [step, setStep] = useState<'phone' | 'code'>('phone');
  const [phone, setPhone] = useState('');
  const [normalized, setNormalized] = useState('');
  const [code, setCode] = useState('');
  const [devCode, setDevCode] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  async function sendCode() {
    setError('');
    setBusy(true);
    try {
      const r = await api.requestCode(phone);
      setNormalized(r.phone);
      setDevCode(r.devCode);
      setStep('code');
    } catch (e) {
      setError((e as ApiError).message);
    } finally {
      setBusy(false);
    }
  }

  async function verify(c = code) {
    setError('');
    setBusy(true);
    try {
      const r = await api.verify(normalized, c);
      await signIn(r.token);
    } catch (e) {
      setError((e as ApiError).message);
      setBusy(false);
    }
  }

  return (
    <SafeAreaView style={{ flex: 1, backgroundColor: t.bg }}>
      <KeyboardAvoidingView behavior={Platform.OS === 'ios' ? 'padding' : undefined} style={{ flex: 1, justifyContent: 'center', padding: 24, gap: 16 }}>
        <View style={{ flexDirection: 'row', height: 36, borderRadius: 8, overflow: 'hidden', marginBottom: 12 }} accessibilityElementsHidden>
          {['#0B4EA2', '#6B46A8', '#137A52', '#B4530F', '#0F7490'].map((c, i) => <View key={c} style={{ flex: [16, 11, 18, 9, 13][i], backgroundColor: c }} />)}
          <View style={{ flex: 10, borderWidth: 2, borderStyle: 'dashed', borderColor: t.faint, borderTopRightRadius: 8, borderBottomRightRadius: 8 }} />
        </View>
        <Eyebrow>Eimsky · Forwarder</Eyebrow>
        {step === 'phone' ? (
          <>
            <H1>Bid on full containers</H1>
            <Body muted>Get a push notification for every new FCL tender on your lanes. Bid in minutes; prices stay sealed.</Body>
            <Field label="Mobile number" value={phone} onChangeText={setPhone} placeholder="+94 77 123 4567" keyboardType="phone-pad" autoComplete="tel" textContentType="telephoneNumber" autoFocus returnKeyType="next" onSubmitEditing={sendCode} testID="phone" />
            {error ? <Banner tone="red" text={error} /> : null}
            <Button title="Send code" onPress={sendCode} loading={busy} disabled={phone.replace(/\D/g, '').length < 9} testID="send-code" />
            <Body small muted>We'll text you a 6-digit code. Standard SMS rates may apply.</Body>
          </>
        ) : (
          <>
            <H1>Enter your code</H1>
            <Body muted>Sent to {normalized}. It expires in 10 minutes.</Body>
            <Field
              label="6-digit code"
              autoFocus
              value={code}
              onChangeText={v => {
                const d = v.replace(/\D/g, '').slice(0, 6);
                setCode(d);
                if (d.length === 6) void verify(d);
              }}
              keyboardType="number-pad"
              autoComplete="sms-otp"
              textContentType="oneTimeCode"
              maxLength={6}
              style={{ fontSize: 24, letterSpacing: 8, textAlign: 'center', fontFamily: 'Courier' }}
              testID="code"
            />
            {devCode ? <Banner tone="amber" text={`Development mode: your code is ${devCode}`} /> : null}
            {error ? <Banner tone="red" text={error} /> : null}
            <Button title="Verify" onPress={() => verify()} loading={busy} disabled={code.length !== 6} testID="verify" />
            <Button title="Use a different number" kind="ghost" onPress={() => { setStep('phone'); setCode(''); setError(''); }} />
            <Text style={{ color: t.primary, textAlign: 'center' }} onPress={sendCode} accessibilityRole="button">Send the code again</Text>
          </>
        )}
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}
