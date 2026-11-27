import Constants from 'expo-constants';
import * as Device from 'expo-device';
import * as Notifications from 'expo-notifications';
import { Platform } from 'react-native';
import { api } from './api';

if (Platform.OS !== 'web') Notifications.setNotificationHandler({
  handleNotification: async () => ({
    shouldPlaySound: true,
    shouldSetBadge: false,
    shouldShowBanner: true,
    shouldShowList: true,
  }),
});

/**
 * Asks for permission and registers this device's Expo push token with Eimsky.
 * Returns a short reason when push isn't available (simulator, web, denied).
 */
export async function registerForPush(): Promise<string | null> {
  if (Platform.OS === 'web') return 'Push notifications work in the phone app.';
  if (!Device.isDevice) return 'Push notifications need a real phone.';
  if (Platform.OS === 'android') {
    await Notifications.setNotificationChannelAsync('tenders', {
      name: 'New tenders and results',
      importance: Notifications.AndroidImportance.MAX,
      lightColor: '#62A420',
    });
  }
  let { status } = await Notifications.getPermissionsAsync();
  if (status !== 'granted') {
    status = (await Notifications.requestPermissionsAsync()).status;
  }
  if (status !== 'granted') return 'Notifications are off. Turn them on in Settings to hear about new tenders.';
  const projectId = (Constants.expoConfig?.extra as any)?.eas?.projectId || Constants.easConfig?.projectId;
  try {
    const token = (await Notifications.getExpoPushTokenAsync(projectId ? { projectId } : undefined)).data;
    await api.savePushToken(token, Platform.OS);
    return null;
  } catch {
    return 'Push notifications need the app built with an Expo project ID.';
  }
}

/** Calls onOpen with the tender id when the user taps a notification. */
export function onNotificationTap(onOpen: (tenderId: number, screen?: string) => void): () => void {
  if (Platform.OS === 'web') return () => {};
  const handle = (data: any) => {
    const id = Number(data?.tenderId);
    if (id) onOpen(id, data?.screen);
  };
  void Notifications.getLastNotificationResponseAsync().then(r => {
    if (r) handle(r.notification.request.content.data);
  });
  const sub = Notifications.addNotificationResponseReceivedListener(r => handle(r.notification.request.content.data));
  return () => sub.remove();
}
