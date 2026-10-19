import { redirect } from "next/navigation";

// Compatibility path for the earlier mock.
export default function InflowRedirect() {
  redirect("/smart-money/inflow");
}
