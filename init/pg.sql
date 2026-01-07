-- create tables
CREATE TABLE ticker_price (
  ticker VARCHAR(10) PRIMARY KEY,
  new_price DECIMAL(10,2) NOT NULL,
  old_price DECIMAL(10,2),
  updated TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE ticker_compare (
  ticker VARCHAR(10) PRIMARY KEY,
  price DECIMAL(10,2)
);

CREATE Table ticker_notify (
  ticker VARCHAR(10) PRIMARY KEY,
  content VARCHAR(20),
  direction VARCHAR(10),
  price DECIMAL(10,2),
  notified TIMESTAMPTZ DEFAULT now()
);

-- set update trigger => update old_price with new_price when new_price changes
CREATE OR REPLACE FUNCTION set_old_price()
RETURNS TRIGGER AS $$
BEGIN
  IF NEW.new_price != OLD.new_price THEN
    NEW.old_price := OLD.new_price;
  END IF;
  NEW.updated := now();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER set_old_price
BEFORE UPDATE ON ticker_price
FOR EACH ROW
EXECUTE FUNCTION set_old_price();

-- set check trigger => update content / direction when direction changes
CREATE OR REPLACE FUNCTION check_price()
RETURNS TRIGGER AS $$
DECLARE
  price_compare DECIMAL(10,2);
  direction VARCHAR(10);
BEGIN
  IF NEW.new_price = OLD.new_price THEN
    RETURN NEW;
  END IF;

  -- get price to compare
  SELECT price INTO price_compare
  FROM ticker_compare
  WHERE ticker = NEW.ticker;

  IF NOT FOUND OR price_compare IS NULL THEN
    RETURN NEW;
  END IF;

  IF NEW.new_price > OLD.new_price THEN
    direction := 'up';
  ELSIF NEW.new_price < OLD.new_price THEN
    direction := 'down';
  ELSE
    RETURN NEW;
  END IF;

  -- compare
  IF direction = 'up'
    AND OLD.new_price < price_compare
    AND NEW.new_price >= price_compare THEN
      -- insert
      INSERT INTO ticker_notify (ticker, content, direction, price)
      VALUES (NEW.ticker, 'up to touch price', direction, NEW.new_price)
      ON CONFLICT (ticker) DO UPDATE
      SET content = EXCLUDED.content,
        direction = EXCLUDED.direction,
        price = EXCLUDED.price,
        notified = now()
      -- only update if direction changed => for backend to notify
      WHERE ticker_notify.direction != EXCLUDED.direction;
  ELSEIF direction = 'down'
    AND OLD.new_price >= price_compare
    AND NEW.new_price < price_compare THEN
      -- insert
      INSERT INTO ticker_notify (ticker, content, direction, price)
      VALUES (NEW.ticker, 'down to the price', direction, NEW.new_price)
      ON CONFLICT (ticker) DO UPDATE
      SET content = EXCLUDED.content,
        direction = EXCLUDED.direction,
        price = EXCLUDED.price,
        notified = now()
      -- only update if direction changed => for backend to notify
      WHERE ticker_notify.direction != EXCLUDED.direction;
  END IF;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER check_price
AFTER UPDATE ON ticker_price
FOR EACH ROW
EXECUTE FUNCTION check_price();

-- set notify trigger => send notification when notified changed
CREATE OR REPLACE FUNCTION notify_ticker_push()
RETURNS TRIGGER AS $$
BEGIN
  PERFORM pg_notify(
    'ticker_notify',
    jsonb_build_object(
      'ticker',    NEW.ticker,
      'content',   NEW.content,
      'direction', NEW.direction,
      'price',     NEW.price
    )::text
  );
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER notify_ticker
AFTER UPDATE ON ticker_notify
FOR EACH ROW
EXECUTE FUNCTION notify_ticker_push();
