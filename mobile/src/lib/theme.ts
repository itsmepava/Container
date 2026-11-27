import { useColorScheme } from 'react-native';

const light = {
  bg: '#EEF2F7',
  card: '#FFFFFF',
  line: '#DBE3EE',
  ink: '#0F172A',
  ink2: '#334155',
  muted: '#64748B',
  faint: '#94A3B8',
  primary: '#0B4EA2',
  primarySoft: '#E8F0FB',
  primaryLine: '#B9CFF0',
  onPrimary: '#FFFFFF',
  green: '#137A52',
  greenSoft: '#E4F3EC',
  amber: '#9A5B00',
  amberSoft: '#FDF3D8',
  red: '#B42318',
  redSoft: '#FDECEA',
  input: '#FFFFFF',
  inputLine: '#C9D4E3',
};

const dark: typeof light = {
  bg: '#0B1220',
  card: '#141D2E',
  line: '#24324A',
  ink: '#E8EEF7',
  ink2: '#C3CEDD',
  muted: '#8A9AB2',
  faint: '#5F708A',
  primary: '#5B9BEA',
  primarySoft: '#16294A',
  primaryLine: '#24457A',
  onPrimary: '#0B1220',
  green: '#4CC38A',
  greenSoft: '#11291F',
  amber: '#E7B04B',
  amberSoft: '#2C2210',
  red: '#F27A6E',
  redSoft: '#2E1513',
  input: '#0F1726',
  inputLine: '#2C3B55',
};

export type Theme = typeof light;

export function useTheme(): Theme {
  return useColorScheme() === 'dark' ? dark : light;
}

export const mono = 'Courier';
