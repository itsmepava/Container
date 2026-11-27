import { useColorScheme } from 'react-native';

const light = {
  bg: '#F4F5F1',
  card: '#FFFFFF',
  line: '#E5E6E0',
  ink: '#292728',
  ink2: '#4A4849',
  muted: '#76747A',
  faint: '#A3A1A5',
  primary: '#292728',
  primarySoft: '#EEF6E3',
  primaryLine: '#CFE5B4',
  onPrimary: '#FFFFFF',
  green: '#4A7D17',
  greenSoft: '#EEF6E3',
  amber: '#9A5800',
  amberSoft: '#FDF4DE',
  red: '#BE3A26',
  redSoft: '#FDEEEB',
  input: '#FFFFFF',
  inputLine: '#D4D6CD',
  brand: '#62A420',
  lime: '#B0CB36',
};

const dark: typeof light = {
  bg: '#121211',
  card: '#1B1A1A',
  line: '#2C2A2A',
  ink: '#F2F1F0',
  ink2: '#D2D0CF',
  muted: '#9B999A',
  faint: '#6E6C6D',
  primary: '#B0CB36',
  primarySoft: '#1C2812',
  primaryLine: '#34501B',
  onPrimary: '#1B1A1A',
  green: '#9BD65C',
  greenSoft: '#1C2812',
  amber: '#E8B04A',
  amberSoft: '#2A210F',
  red: '#F08A78',
  redSoft: '#2E1612',
  input: '#211F1F',
  inputLine: '#3A3838',
  brand: '#7DC23A',
  lime: '#C2DC4A',
};

export type Theme = typeof light;

export function useTheme(): Theme {
  return useColorScheme() === 'dark' ? dark : light;
}

export const mono = 'Courier';
