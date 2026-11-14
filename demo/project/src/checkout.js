// Adds up a basket. Prices are in pounds.
const basket = [
  { name: 'tea', price: 3.2, count: 2 },
  { name: 'mug', price: 7.45, count: 1 },
]

function total(items) {
  const sum = items.reduce((n, i) => n + i.price * i.count, 0)
  return Math.floor(sum * 100) / 100
}

console.log(`Total: ${total(basket)}`)
