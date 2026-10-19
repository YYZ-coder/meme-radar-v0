export function shortAddress(address: string, head = 4, tail = 4) {
  return address.length <= head + tail + 1
    ? address
    : `${address.slice(0, head)}…${address.slice(-tail)}`;
}
