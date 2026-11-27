import Ionicons from '@expo/vector-icons/Ionicons';
import { Link, Tabs } from 'expo-router';
import React, { useCallback, useState } from 'react';
import { Pressable, Text, View } from 'react-native';
import { useFocusEffect } from 'expo-router';
import { api } from '@/lib/api';
import { useTheme } from '@/lib/theme';

function Bell() {
  const t = useTheme();
  const [unread, setUnread] = useState(0);
  useFocusEffect(
    useCallback(() => {
      api.notifications().then(r => setUnread(r.unread)).catch(() => {});
    }, []),
  );
  return (
    <Link href="/notifications" asChild>
      <Pressable accessibilityLabel={`Notifications, ${unread} unread`} style={{ paddingHorizontal: 16, paddingVertical: 6 }}>
        <Ionicons name="notifications-outline" size={22} color={t.ink} />
        {unread > 0 ? (
          <View style={{ position: 'absolute', top: 0, right: 10, minWidth: 17, height: 17, borderRadius: 9, backgroundColor: t.red, alignItems: 'center', justifyContent: 'center', paddingHorizontal: 4 }}>
            <Text style={{ color: '#fff', fontSize: 10, fontWeight: '800' }}>{unread > 99 ? '99+' : unread}</Text>
          </View>
        ) : null}
      </Pressable>
    </Link>
  );
}

export default function TabsLayout() {
  const t = useTheme();
  const icon = (name: keyof typeof Ionicons.glyphMap) => ({ color, size }: { color: any; size: number }) => <Ionicons name={name} color={color} size={size} />;
  return (
    <Tabs
      screenOptions={{
        tabBarActiveTintColor: t.primary,
        tabBarInactiveTintColor: t.muted,
        tabBarStyle: { backgroundColor: t.card, borderTopColor: t.line },
        headerStyle: { backgroundColor: t.card },
        headerTitleStyle: { color: t.ink, fontWeight: '700' },
        headerRight: () => <Bell />,
      }}>
      <Tabs.Screen name="index" options={{ title: 'Tenders', headerTitle: 'Open FCL tenders', tabBarIcon: icon('cube-outline') }} />
      <Tabs.Screen name="bids" options={{ title: 'My bids', tabBarIcon: icon('pricetags-outline') }} />
      <Tabs.Screen name="awarded" options={{ title: 'Awarded', tabBarIcon: icon('trophy-outline') }} />
      <Tabs.Screen name="profile" options={{ title: 'Profile', tabBarIcon: icon('person-circle-outline') }} />
    </Tabs>
  );
}
