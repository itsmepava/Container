import { useFocusEffect } from 'expo-router';
import React, { useCallback, useState } from 'react';
import { FlatList, RefreshControl, View } from 'react-native';
import { TenderCard } from '@/components/TenderCard';
import { Banner, Body, Empty, Loading } from '@/components/ui';
import { api, Tender } from '@/lib/api';
import { useTheme } from '@/lib/theme';

/** A pull-to-refresh list of tenders for one scope, refreshed on focus and every 30 s. */
export function TenderList({ scope, header, empty, hrefFor }: { scope: 'open' | 'mine' | 'awarded'; header?: React.ReactNode; empty: { title: string; body: string }; hrefFor?: (t: Tender) => string }) {
  const t = useTheme();
  const [items, setItems] = useState<Tender[] | null>(null);
  const [error, setError] = useState('');
  const [refreshing, setRefreshing] = useState(false);

  const load = useCallback(async () => {
    try {
      setItems(await api.tenders(scope));
      setError('');
    } catch (e) {
      setError((e as Error).message);
    }
  }, [scope]);

  useFocusEffect(
    useCallback(() => {
      void load();
      const id = setInterval(load, 30000);
      return () => clearInterval(id);
    }, [load]),
  );

  if (items === null && !error) return <Loading />;
  return (
    <FlatList
      style={{ backgroundColor: t.bg }}
      contentContainerStyle={{ padding: 16, gap: 12, flexGrow: 1 }}
      data={items ?? []}
      keyExtractor={x => String(x.id)}
      renderItem={({ item }) => <TenderCard tender={item} href={hrefFor?.(item)} />}
      refreshControl={<RefreshControl refreshing={refreshing} onRefresh={async () => { setRefreshing(true); await load(); setRefreshing(false); }} />}
      ListHeaderComponent={<View style={{ gap: 12 }}>{error ? <Banner tone="red" text={error} /> : null}{header}</View>}
      ListEmptyComponent={<Empty title={empty.title} body={empty.body} />}
    />
  );
}
