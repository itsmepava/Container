import React from 'react';
import {
  ActivityIndicator,
  Pressable,
  ScrollView,
  ScrollViewProps,
  StyleProp,
  StyleSheet,
  Text,
  TextInput,
  TextInputProps,
  TextStyle,
  View,
  ViewStyle,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { useTheme } from '@/lib/theme';

export function Screen({ children, scroll = true, refresh, edges, ...rest }: { children: React.ReactNode; scroll?: boolean; refresh?: React.ReactElement<any>; edges?: ('top' | 'bottom')[] } & ScrollViewProps) {
  const t = useTheme();
  return (
    <SafeAreaView style={{ flex: 1, backgroundColor: t.bg }} edges={edges ?? ['bottom']}>
      {scroll ? (
        <ScrollView contentContainerStyle={{ padding: 16, gap: 12, paddingBottom: 32 }} keyboardShouldPersistTaps="handled" refreshControl={refresh} {...rest}>
          {children}
        </ScrollView>
      ) : (
        <View style={{ flex: 1, padding: 16, gap: 12 }}>{children}</View>
      )}
    </SafeAreaView>
  );
}

export function Card({ children, style, tone }: { children: React.ReactNode; style?: StyleProp<ViewStyle>; tone?: 'accent' | 'good' | 'warn' | 'bad' }) {
  const t = useTheme();
  const bg = tone === 'good' ? t.greenSoft : tone === 'warn' ? t.amberSoft : tone === 'bad' ? t.redSoft : t.card;
  const border = tone === 'accent' ? t.primary : t.line;
  return <View style={[{ backgroundColor: bg, borderColor: border, borderWidth: tone === 'accent' ? 1.5 : 1, borderRadius: 14, padding: 16 }, style]}>{children}</View>;
}

export function Eyebrow({ children, color }: { children: React.ReactNode; color?: string }) {
  const t = useTheme();
  return <Text style={{ fontSize: 11, fontWeight: '700', letterSpacing: 0.9, textTransform: 'uppercase', color: color ?? t.primary, fontFamily: 'Courier' }}>{children}</Text>;
}

export function H1({ children, style }: { children: React.ReactNode; style?: StyleProp<TextStyle> }) {
  const t = useTheme();
  return <Text style={[{ fontSize: 24, fontWeight: '800', color: t.ink, letterSpacing: -0.4 }, style]}>{children}</Text>;
}

export function H2({ children, style }: { children: React.ReactNode; style?: StyleProp<TextStyle> }) {
  const t = useTheme();
  return <Text style={[{ fontSize: 17, fontWeight: '700', color: t.ink }, style]}>{children}</Text>;
}

export function Body({ children, muted, small, style }: { children: React.ReactNode; muted?: boolean; small?: boolean; style?: StyleProp<TextStyle> }) {
  const t = useTheme();
  return <Text style={[{ fontSize: small ? 13 : 15, lineHeight: small ? 18 : 21, color: muted ? t.muted : t.ink2 }, style]}>{children}</Text>;
}

export function Big({ children, color }: { children: React.ReactNode; color?: string }) {
  const t = useTheme();
  return <Text style={{ fontSize: 34, fontWeight: '800', color: color ?? t.ink, letterSpacing: -0.8 }}>{children}</Text>;
}

export function Button({ title, onPress, kind = 'primary', loading, disabled, style, testID }: { title: string; onPress: () => void; kind?: 'primary' | 'secondary' | 'danger' | 'ghost'; loading?: boolean; disabled?: boolean; style?: StyleProp<ViewStyle>; testID?: string }) {
  const t = useTheme();
  const off = disabled || loading;
  const bg = kind === 'primary' ? t.primary : kind === 'ghost' ? 'transparent' : t.card;
  const fg = kind === 'primary' ? t.onPrimary : kind === 'danger' ? t.red : t.primary;
  return (
    <Pressable
      testID={testID}
      accessibilityRole="button"
      accessibilityState={{ disabled: !!off, busy: !!loading }}
      onPress={off ? undefined : onPress}
      style={({ pressed }) => [
        { minHeight: 52, borderRadius: 12, alignItems: 'center', justifyContent: 'center', paddingHorizontal: 18, backgroundColor: bg, borderWidth: kind === 'primary' || kind === 'ghost' ? 0 : 1, borderColor: kind === 'danger' ? t.red : t.line, opacity: off ? 0.5 : pressed ? 0.85 : 1 },
        style,
      ]}>
      {loading ? <ActivityIndicator color={fg} /> : <Text style={{ color: fg, fontWeight: '700', fontSize: 16 }}>{title}</Text>}
    </Pressable>
  );
}

export function Field({ label, help, error, ...input }: { label: string; help?: string; error?: string } & TextInputProps) {
  const t = useTheme();
  return (
    <View style={{ gap: 6 }}>
      <Text style={{ fontSize: 13, fontWeight: '600', color: t.ink2 }}>{label}</Text>
      <TextInput
        placeholderTextColor={t.faint}
        {...input}
        style={[{ minHeight: 50, borderWidth: 1, borderColor: error ? t.red : t.inputLine, borderRadius: 10, paddingHorizontal: 14, fontSize: 16, color: t.ink, backgroundColor: t.input }, input.style]}
      />
      {error ? <Text style={{ color: t.red, fontSize: 13 }}>{error}</Text> : help ? <Text style={{ color: t.muted, fontSize: 13 }}>{help}</Text> : null}
    </View>
  );
}

export function Pill({ label, tone }: { label: string; tone?: 'blue' | 'green' | 'amber' | 'red' }) {
  const t = useTheme();
  const map = { blue: [t.primarySoft, t.primary], green: [t.greenSoft, t.green], amber: [t.amberSoft, t.amber], red: [t.redSoft, t.red] } as const;
  const [bg, fg] = tone ? map[tone] : [t.bg, t.ink2];
  return (
    <View style={{ backgroundColor: bg, borderRadius: 999, paddingHorizontal: 9, paddingVertical: 3, alignSelf: 'flex-start' }}>
      <Text style={{ color: fg, fontSize: 11, fontWeight: '700', letterSpacing: 0.5, fontFamily: 'Courier' }}>{label.toUpperCase()}</Text>
    </View>
  );
}

export function KV({ k, v, last }: { k: string; v: React.ReactNode; last?: boolean }) {
  const t = useTheme();
  return (
    <View style={{ flexDirection: 'row', justifyContent: 'space-between', gap: 12, paddingVertical: 10, borderBottomWidth: last ? 0 : StyleSheet.hairlineWidth, borderColor: t.line }}>
      <Text style={{ color: t.muted, fontSize: 14 }}>{k}</Text>
      {typeof v === 'string' ? <Text style={{ color: t.ink, fontSize: 14, fontWeight: '600', flexShrink: 1, textAlign: 'right' }}>{v}</Text> : v}
    </View>
  );
}

export function Row({ children, style }: { children: React.ReactNode; style?: StyleProp<ViewStyle> }) {
  return <View style={[{ flexDirection: 'row', alignItems: 'center', gap: 10 }, style]}>{children}</View>;
}

export function Banner({ text, tone = 'blue' }: { text: string; tone?: 'blue' | 'green' | 'amber' | 'red' }) {
  const t = useTheme();
  const map = { blue: [t.primarySoft, t.primary], green: [t.greenSoft, t.green], amber: [t.amberSoft, t.amber], red: [t.redSoft, t.red] } as const;
  const [bg, fg] = map[tone];
  return (
    <View accessibilityRole="alert" style={{ backgroundColor: bg, borderRadius: 10, padding: 12 }}>
      <Text style={{ color: fg, fontSize: 14, lineHeight: 20 }}>{text}</Text>
    </View>
  );
}

export function Empty({ title, body, action }: { title: string; body: string; action?: React.ReactNode }) {
  const t = useTheme();
  return (
    <View style={{ alignItems: 'center', paddingVertical: 48, paddingHorizontal: 24, gap: 8 }}>
      <Text style={{ fontSize: 17, fontWeight: '700', color: t.ink, textAlign: 'center' }}>{title}</Text>
      <Text style={{ fontSize: 14, color: t.muted, textAlign: 'center', lineHeight: 20 }}>{body}</Text>
      {action}
    </View>
  );
}

export function Loading() {
  const t = useTheme();
  return (
    <View style={{ flex: 1, alignItems: 'center', justifyContent: 'center', backgroundColor: t.bg }}>
      <ActivityIndicator color={t.primary} />
    </View>
  );
}

export function Check({ label, checked, onToggle, sub }: { label: string; checked: boolean; onToggle: () => void; sub?: string }) {
  const t = useTheme();
  return (
    <Pressable accessibilityRole="checkbox" accessibilityState={{ checked }} onPress={onToggle} style={{ flexDirection: 'row', alignItems: 'center', gap: 12, paddingVertical: 8 }}>
      <View style={{ width: 24, height: 24, borderRadius: 6, borderWidth: 2, borderColor: checked ? t.primary : t.inputLine, backgroundColor: checked ? t.primary : 'transparent', alignItems: 'center', justifyContent: 'center' }}>
        {checked ? <Text style={{ color: t.onPrimary, fontWeight: '900', fontSize: 14 }}>✓</Text> : null}
      </View>
      <View style={{ flex: 1 }}>
        <Text style={{ fontSize: 15, color: t.ink }}>{label}</Text>
        {sub ? <Text style={{ fontSize: 13, color: t.muted }}>{sub}</Text> : null}
      </View>
    </Pressable>
  );
}

export function Radio({ label, sub, selected, onPress, badge }: { label: string; sub?: string; selected: boolean; onPress: () => void; badge?: string }) {
  const t = useTheme();
  return (
    <Pressable accessibilityRole="radio" accessibilityState={{ selected }} onPress={onPress}
      style={{ flexDirection: 'row', gap: 12, padding: 12, borderRadius: 12, borderWidth: selected ? 1.5 : 1, borderColor: selected ? t.primary : t.line, backgroundColor: selected ? t.primarySoft : t.card }}>
      <View style={{ width: 20, height: 20, marginTop: 2, borderRadius: 10, borderWidth: 2, borderColor: selected ? t.primary : t.inputLine, alignItems: 'center', justifyContent: 'center' }}>
        {selected ? <View style={{ width: 10, height: 10, borderRadius: 5, backgroundColor: t.primary }} /> : null}
      </View>
      <View style={{ flex: 1, gap: 2 }}>
        <Row><Text style={{ fontSize: 15, fontWeight: '700', color: t.ink, flexShrink: 1 }}>{label}</Text>{badge ? <Pill label={badge} tone="blue" /> : null}</Row>
        {sub ? <Text style={{ fontSize: 13, color: t.muted }}>{sub}</Text> : null}
      </View>
    </Pressable>
  );
}

/** A document row with status and an upload action. */
export function DocRow({ label, sub, done, action }: { label: string; sub: string; done: boolean; action?: React.ReactNode }) {
  const t = useTheme();
  return (
    <View style={{ flexDirection: 'row', alignItems: 'center', gap: 12, paddingVertical: 12, borderBottomWidth: StyleSheet.hairlineWidth, borderColor: t.line }}>
      <View style={{ width: 34, height: 34, borderRadius: 8, backgroundColor: done ? t.greenSoft : t.bg, alignItems: 'center', justifyContent: 'center' }}>
        <Text style={{ color: done ? t.green : t.muted, fontWeight: '800' }}>{done ? '✓' : '·'}</Text>
      </View>
      <View style={{ flex: 1 }}>
        <Text style={{ fontSize: 15, fontWeight: '600', color: t.ink }}>{label}</Text>
        <Text style={{ fontSize: 13, color: t.muted }} numberOfLines={1}>{sub}</Text>
      </View>
      {action}
    </View>
  );
}
