import { useRouter } from 'expo-router';
import React from 'react';
import { Pressable, Text, View } from 'react-native';
import { Pill, Row } from '@/components/ui';
import { Tender } from '@/lib/api';
import { day, money, until } from '@/lib/format';
import { useTheme } from '@/lib/theme';

function Stat({ label, value }: { label: string; value: string }) {
  const t = useTheme();
  return (
    <View style={{ flex: 1 }}>
      <Text style={{ fontSize: 16, fontWeight: '800', color: t.ink }}>{value}</Text>
      <Text style={{ fontSize: 12, color: t.muted }}>{label}</Text>
    </View>
  );
}

export function TenderCard({ tender, href }: { tender: Tender; href?: string }) {
  const t = useTheme();
  const router = useRouter();
  const target = href ?? `/tender/${tender.id}`;
  let pill: React.ReactNode = null;
  if (tender.result === 'won') pill = <Pill label="Awarded" tone="green" />;
  else if (tender.result === 'lost') pill = <Pill label="Not won" />;
  else if (tender.open && !tender.myBid) pill = <Pill label="New" tone="blue" />;
  else if (tender.open) pill = <Pill label="Bid placed" tone="green" />;
  else if (tender.status === 'closed') pill = <Pill label="Bids closed" tone="amber" />;

  return (
      <Pressable accessibilityRole="button" accessibilityLabel={`${tender.lane}, ${tender.ref}`} onPress={() => router.push(target as any)} style={({ pressed }) => ({ backgroundColor: t.card, borderRadius: 14, borderWidth: 1, borderColor: t.line, padding: 16, gap: 12, opacity: pressed ? 0.85 : 1 })}>
        <Row style={{ justifyContent: 'space-between', alignItems: 'flex-start' }}>
          <View style={{ flex: 1 }}>
            <Text style={{ fontSize: 17, fontWeight: '700', color: t.ink }}>{tender.lane}</Text>
            <Text style={{ fontSize: 13, color: t.muted, marginTop: 2 }}>
              {tender.ref} · Week {tender.week}{tender.vessel ? ` · ETD ${day(tender.vessel.etd)}` : ''}
            </Text>
          </View>
          {pill}
        </Row>
        <Row>
          <Stat label="container" value={`1 × ${tender.boxCode}`} />
          <Stat label="volume" value={`${tender.cbm.toFixed(1)} CBM`} />
          <Stat label="weight" value={`${tender.tonnes.toFixed(1)} t`} />
        </Row>
        {tender.result === 'won' ? (
          <Text style={{ color: t.green, fontWeight: '600' }}>You won at USD {money(tender.wonAmount ?? 0)} · see shippers and upload your booking confirmation</Text>
        ) : tender.open ? (
          <Text style={{ color: t.amber, fontWeight: '600' }}>
            Closes in {until(tender.closesAt)}{tender.myBid ? ` · your bid USD ${money(tender.myBid.amount)}` : ''}
          </Text>
        ) : tender.myBid ? (
          <Text style={{ color: t.muted }}>Your bid USD {money(tender.myBid.amount)}</Text>
        ) : null}
      </Pressable>
  );
}
