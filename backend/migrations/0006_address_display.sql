-- Addresses, hashes and signatures stay raw bytea; these helpers only render
-- and parse the chain's native text form for people querying the database:
-- Solana base58 (Bitcoin alphabet), every other chain lowercase 0x hex.
--   SELECT addr(chain, wallet) FROM trades WHERE wallet = from_addr('solana', '6EF8…');
CREATE OR REPLACE FUNCTION addr(chain chain_id, b bytea) RETURNS text
LANGUAGE plpgsql IMMUTABLE STRICT PARALLEL SAFE AS $$
DECLARE
  alphabet constant text := '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz';
  n numeric := 0;
  res text := '';
  zeros int := 0;
BEGIN
  IF chain::text <> 'solana' THEN
    RETURN '0x' || encode(b, 'hex');
  END IF;
  FOR i IN 0 .. length(b) - 1 LOOP
    n := n * 256 + get_byte(b, i);
  END LOOP;
  WHILE n > 0 LOOP
    res := substr(alphabet, mod(n, 58)::int + 1, 1) || res;
    n := div(n, 58);
  END LOOP;
  WHILE zeros < length(b) AND get_byte(b, zeros) = 0 LOOP
    zeros := zeros + 1;
  END LOOP;
  RETURN repeat('1', zeros) || res;
END $$;

CREATE OR REPLACE FUNCTION from_addr(chain chain_id, s text) RETURNS bytea
LANGUAGE plpgsql IMMUTABLE STRICT PARALLEL SAFE AS $$
DECLARE
  alphabet constant text := '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz';
  n numeric := 0;
  d int;
  res bytea := ''::bytea;
  zeros int := 0;
BEGIN
  IF chain::text <> 'solana' THEN
    RETURN decode(regexp_replace(lower(s), '^0x', ''), 'hex');
  END IF;
  FOR i IN 1 .. length(s) LOOP
    d := strpos(alphabet, substr(s, i, 1)) - 1;
    IF d < 0 THEN
      RAISE EXCEPTION 'invalid base58 character "%" in "%"', substr(s, i, 1), s USING ERRCODE = '22023';
    END IF;
    n := n * 58 + d;
  END LOOP;
  WHILE n > 0 LOOP
    res := set_byte('\x00'::bytea, 0, mod(n, 256)::int) || res;
    n := div(n, 256);
  END LOOP;
  WHILE zeros < length(s) AND substr(s, zeros + 1, 1) = '1' LOOP
    zeros := zeros + 1;
  END LOOP;
  RETURN decode(repeat('00', zeros), 'hex') || res;
END $$;
