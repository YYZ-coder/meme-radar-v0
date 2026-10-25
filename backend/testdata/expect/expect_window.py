"""Independent Decimal expectations from the H1 hand-calculated fixture table."""
from decimal import Decimal, localcontext, ROUND_HALF_UP
from pathlib import Path
import json

cases = json.loads(Path(__file__).with_name("window_cases.json").read_text())
quantum = Decimal(1).scaleb(-cases["scale"])
def decimal_text(value):
    text = format(value, "f")
    return text.rstrip("0").rstrip(".") if "." in text else text

result = {}
with localcontext() as context:
    context.prec = 100
    for currency in ["usd", "native"]:
        changes = {}
        for case in cases["price_cases"]:
            current = case.get("native_current", case["current"]) if currency == "native" else case["current"]
            bases = case.get("native_bases", case["bases"]) if currency == "native" else case["bases"]
            expected = []
            for base in bases:
                if current is None or base is None or Decimal(base) == 0:
                    expected.append(None)
                else:
                    percent = (Decimal(current) - Decimal(base)) / Decimal(base) * 100
                    expected.append(decimal_text(percent.quantize(quantum, rounding=ROUND_HALF_UP)))
            changes[case["name"]] = expected
        result["price_changes" if currency == "usd" else "native_price_changes"] = changes
with localcontext() as context:
    context.prec = 100
    candle = cases["candle_case"]
    result["market_cap"] = {currency: [
        [decimal_text(Decimal(price) * Decimal(candle["supply"])) for price in bar]
        for bar in candle[currency + "_ohlc"]
    ] for currency in ["usd", "native"]}
print(json.dumps(result, sort_keys=True))
