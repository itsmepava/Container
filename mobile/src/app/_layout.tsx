import { Stack, useRouter, useSegments } from 'expo-router';
import { StatusBar } from 'expo-status-bar';
import React, { useEffect } from 'react';
import { useColorScheme } from 'react-native';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import { Loading } from '@/components/ui';
import { AuthProvider, homeFor, useAuth } from '@/lib/auth';
import { onNotificationTap, registerForPush } from '@/lib/push';
import { useTheme } from '@/lib/theme';

function Gate() {
  const { ready, signedIn, me } = useAuth();
  const segments = useSegments();
  const router = useRouter();
  const t = useTheme();

  // Keep people on the right screens for their sign-in and approval state.
  useEffect(() => {
    if (!ready) return;
    const first = segments[0] as string | undefined;
    if (!signedIn) {
      if (first !== 'sign-in') router.replace('/sign-in');
      return;
    }
    if (!me) return; // profile still loading
    const home = homeFor(me);
    const inApp = first === '(tabs)' || first === 'tender' || first === 'award' || first === 'notifications';
    if (home === '/(tabs)') {
      if (!inApp) router.replace('/(tabs)');
    } else if (first !== home.slice(1) && !(home === '/pending' && first === 'register')) {
      router.replace(home);
    }
  }, [ready, signedIn, me, segments, router]);

  // Register for push once approved, and open tenders from notification taps.
  const approved = me?.user.companyStatus === 'approved';
  useEffect(() => {
    if (!approved) return;
    void registerForPush();
    return onNotificationTap((id, screen) => router.push(screen === 'award' ? `/award/${id}` : `/tender/${id}`));
  }, [approved, router]);

  if (!ready) return <Loading />;
  return (
    <Stack
      screenOptions={{
        headerStyle: { backgroundColor: t.card },
        headerTintColor: t.primary,
        headerTitleStyle: { color: t.ink, fontWeight: '700' },
        contentStyle: { backgroundColor: t.bg },
        headerBackButtonDisplayMode: 'minimal',
      }}>
      <Stack.Screen name="index" options={{ headerShown: false }} />
      <Stack.Screen name="sign-in" options={{ headerShown: false }} />
      <Stack.Screen name="register" options={{ title: 'Register to bid' }} />
      <Stack.Screen name="pending" options={{ title: 'Registration', headerBackVisible: false }} />
      <Stack.Screen name="(tabs)" options={{ headerShown: false }} />
      <Stack.Screen name="tender/[id]" options={{ title: 'Tender' }} />
      <Stack.Screen name="award/[id]" options={{ title: 'Awarded' }} />
      <Stack.Screen name="notifications" options={{ title: 'Notifications' }} />
    </Stack>
  );
}

export default function RootLayout() {
  const scheme = useColorScheme();
  return (
    <SafeAreaProvider>
      <AuthProvider>
        <StatusBar style={scheme === 'dark' ? 'light' : 'dark'} />
        <Gate />
      </AuthProvider>
    </SafeAreaProvider>
  );
}
