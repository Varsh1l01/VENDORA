
set -e  # Exit on any error

# Create a database if it doesn't exist
create_database() {
    local database=$1
    echo "Creating database: $database"
    psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" <<-EOSQL
        SELECT 'CREATE DATABASE $database'
        WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '$database')\gexec
EOSQL
}

# Create all the databases we need
# Each service gets its own database
create_database "users_db"          # For user-service: users, addresses
create_database "products_db"       # For product-service: products, categories
create_database "orders_db"         # For order-service: orders, order_items
create_database "payments_db"       # For payment-service: payments, refunds
create_database "inventory_db"      # For inventory-service: stock levels
create_database "notifications_db"  # For notification-service: notification logs

echo "All databases created successfully!"
