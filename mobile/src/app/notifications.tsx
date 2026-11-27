import { useRouter } from 'expo-router';
import React, { useCallback, useEffect, useState } from 'react';
import { FlatList, Pressable, RefreshControl, Text, View } from 'react-native';
import { Button, Empty, Loading } from '@/components/ui';
import { api, Notice } from '@/lib/api';
import { ago } from '@/lib/format';
import { useTheme } from '@/lib/theme';

export default function Notifications() {
  const t = useTheme();
  const [items, setItems] = useState<Notice[] | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const load = useCallback(async () => {
    try {
      setItems((await api.notifications()).items);
    } catch {
      setItems([]);
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  if (!items) return <Loading />;
  return (
    <FlatList
      style={{ backgroundColor: t.bg }}
      contentContainerStyle={{ padding: 16, gap: 8, flexGrow: 1 }}
      data={items}
      keyExtractor={n => String(n.id)}
      refreshControl={<RefreshControl refreshing={refreshing} onRefresh={async () => { setRefreshing(true); await load(); setRefreshing(false); }} />}
      ListHeaderComponent={items.some(n => !n.read) ? <Button title="Mark all as read" kind="ghost" onPress={async () => { await api.markRead(0); await load(); }} /> : undefined}
      ListEmptyComponent={<Empty title="You're all caught up" body="New tenders, results and approvals show up here." />}
      renderItem={({ item }) => (
        <Pressable onPress={async () => { if (!item.read) { await api.markRead(item.id); void load(); } }}
          style={{ backgroundColor: item.read ? t.card : t.primarySoft, borderRadius: 12, borderWidth: 1, borderColor: t.line, padding: 14, flexDirection: 'row', gap: 10 }}>
          <View style={{ width: 8, height: 8, borderRadius: 4, marginTop: 7, backgroundColor: item.read ? 'transparent' : t.primary }} />
          <View style={{ flex: 1 }}>
            <Text style={{ fontWeight: item.read ? '500' : '700', color: t.ink, fontSize: 15 }}>{item.title}</Text>
            {item.body ? <Text style={{ color: t.muted, marginTop: 2 }}>{item.body}</Text> : null}
            <Text style={{ color: t.faint, fontSize: 12, marginTop: 4 }}>{ago(item.createdAt)}</Text>
          </View>
        </Pressable>
      )}
    />
  );
}
